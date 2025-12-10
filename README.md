# Firehose for TRON

## Project Overview

The Firehose for TRON project (`firetron` CLI) includes the following commands:
- `firetron fetch`: Pulls native TRON blocks from an RPC endpoint and converts them to Firehose format, to be consumed by [firecore](https://github.com/streamingfast/firehose-core).
- `firetron fetch-evm`: Pulls blocks from an RPC endpoint and converts them to an EVM-compatible Firehose block format, to be consumed by [fireeth](https://github.com/streamingfast/firehose-ethereum).
- `firetron test-block`: Used for testing block fetching.

The Firehose TRON currently **does not handle block persistence or merging by itself**. It only implements the *reader* logic, fetching blocks from a TRON node and emitting a Firehose-compatible data through `stdout`. The `firetron fetch/fetch-evm` commands aren't expected to be run standalone and instead they are expected to by spin up from `firecore/fireeth start ...`. To enable persistence (`one-block` files) and bundle merging (`merged-blocks`), you need to run `firetron` **as the reader-node** inside **firecore**, which provides the rest of the Firehose/Substreams pipeline:

- `reader-node` → Runs `firetron fetch ...` and writes one-blocks
- `merger` → Merges one-blocks into merged-blocks
- `relayer` → Provides a live stream of blocks for Firehose & Substreams gRPC servers
- `firehose` gRPC server → Provides the [Firehose gRPC service](https://buf.build/streamingfast/firehose/docs/main:sf.firehose.v2#sf.firehose.v2.Stream)
- `substreams` gRPC server → Provides the [Substreams gRPC service](https://docs.substreams.dev)

### Example

1. **Use `firetron fetch` as the reader-node**  
   In `sf-tron-mainnet.yaml`:

   ```yaml
   # `firetron` is expected to be found in your PATH, use absolute path if it's not the case
   reader-node-path: firetron
   reader-node-arguments: |
     fetch <start_block>
     --state-dir=/data/tron/firehose/state
     --interval-between-fetch=50ms
     --tron-endpoints=http://127.0.0.1:50051

2.	Let firecore run the full pipeline

   ```shell
   firecore -c sf-tron-mainnet.yaml start
   ```

This successfully produced:
- one-block files
- merged-block bundles
- a working Firehose gRPC endpoint

## References

```
firetron fetch 75748000 --state-dir /persistent/path --block-fetch-batch-size=1 --interval-between-fetch=0s --tron-endpoints=http://my.tron.endpoint --tron-api-key=xxxxxxxxx
```

```
firetron fetch-evm 0 --tron-evm-endpoints=https://provider/jsonrpc --block-fetch-batch-size=1 --interval-between-fetch=1s --tron-endpoints=http://tron.grpc.endpoint:12345 --state-dir=/persistent/path --tron-api-key=xxxxxxx
```

> [!NOTE]
> To be wrapped by `firecore/fireeth`, see [Example](#example) section for details.
