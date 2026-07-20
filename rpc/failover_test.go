package rpc

import (
	"context"
	"net"
	"testing"
	"time"

	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	pbtronapi "github.com/streamingfast/tron-protocol/pb/api"
	pbtroncore "github.com/streamingfast/tron-protocol/pb/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type recordingWalletServer struct {
	pbtronapi.UnimplementedWalletServer
	name    string
	failNow bool
	gotKeys []string
}

func (s *recordingWalletServer) GetNowBlock2(ctx context.Context, _ *pbtronapi.EmptyMessage) (*pbtronapi.BlockExtention, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		vals := md.Get("tron-pro-api-key")
		if len(vals) > 0 {
			s.gotKeys = append(s.gotKeys, vals[0])
		}
	}
	if s.failNow {
		return nil, status.Error(codes.Unavailable, "provider "+s.name+" down")
	}
	return &pbtronapi.BlockExtention{
		BlockHeader: &pbtroncore.BlockHeader{
			RawData: &pbtroncore.BlockHeaderRaw{Number: 42},
		},
	}, nil
}

func startWalletServer(t *testing.T, srv *recordingWalletServer, key string) pbtronapi.WalletClient {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	pbtronapi.RegisterWalletServer(grpcServer, srv)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(&apiKeyCredentials{apiKey: key}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return pbtronapi.NewWalletClient(conn)
}

func TestFailoverReachesSecondProvider(t *testing.T) {
	srvA := &recordingWalletServer{name: "A", failNow: true}
	srvB := &recordingWalletServer{name: "B", failNow: false}

	clientA := startWalletServer(t, srvA, "KEY_A")
	clientB := startWalletServer(t, srvB, "KEY_B")

	strategy := firecoreRPC.NewStickyRollingStrategy[pbtronapi.WalletClient]()
	clients := firecoreRPC.NewClients(2*time.Second, strategy, zlogTest)
	clients.Add(clientA)
	clients.Add(clientB)

	block, err := firecoreRPC.WithClientsContext(clients, context.Background(),
		func(ctx context.Context, c pbtronapi.WalletClient) (*pbtronapi.BlockExtention, error) {
			return c.GetNowBlock2(ctx, &pbtronapi.EmptyMessage{})
		},
	)
	require.NoError(t, err)
	require.NotNil(t, block)
	assert.Equal(t, int64(42), block.BlockHeader.RawData.Number)

	// Each server saw only its own key.
	assert.Equal(t, []string{"KEY_A"}, srvA.gotKeys)
	assert.Equal(t, []string{"KEY_B"}, srvB.gotKeys)
}
