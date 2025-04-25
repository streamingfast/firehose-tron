package main

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/streamingfast/cli"
	"github.com/streamingfast/dgrpc"
	"github.com/streamingfast/firehose-tron/tron/pb/api"
	"github.com/streamingfast/firehose-tron/tron/pb/core"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func main() {

	// Testing just `raw_data_bytes` decoding
	raw := "0a02423f2208472f24de7c9fdadb40d8ace1bdce2c5a68080112640a2d747970652e676f6f676c65617069732e636f6d2f70726f746f636f6c2e5472616e73666572436f6e747261637412330a154108beabccd6013f7297ab81b6e4d6b89c69ba98cd12154137eb42342e390fb0299059b4487fa98c7aa8aa5718f6c9d10470ebdcddbdce2c"
	rawBytes, err := hex.DecodeString(raw)
	cli.NoError(err, "Failed to decode hex string")

	trxRaw := &core.TransactionRaw{}
	cli.NoError(proto.Unmarshal(rawBytes, trxRaw), "Failed to unmarshal raw bytes to TransactionRaw")

	printProtoToMultilineJSON(trxRaw)

	conn, err := dgrpc.NewExternalClientConn("grpc.trongrid.io:50051", dgrpc.WithMustAutoTransportCredentials(false, true, false))
	cli.NoError(err, "Failed to create client connection")

	client := api.NewWalletClient(conn)

	block, err := client.GetBlockByNum2(context.Background(), &api.NumberMessage{Num: 1000000})
	cli.NoError(err, "Failed to get block by number")

	printProtoToMultilineJSON(block)

	trxInfoList, err := client.GetTransactionInfoByBlockNum(context.Background(), &api.NumberMessage{Num: 1000000})
	cli.NoError(err, "Failed to get transaction info by block number")

	printProtoToMultilineJSON(trxInfoList)
}

func printProtoToMultilineJSON(message proto.Message) {
	marshaler := protojson.MarshalOptions{
		Multiline: true,
		Indent:    "  ",
	}
	jsonBytes, err := marshaler.Marshal(message)
	cli.NoError(err, "Failed to marshal proto message to JSON")

	fmt.Println(string(jsonBytes))
}
