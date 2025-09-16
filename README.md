# Firehose poller for TRON

* supports native tron blocks via `fetch`
* support EVM model via `fetch-evm`

ex:
```
firetron fetch 75748000 --state-dir /persistent/path --block-fetch-batch-size=1 --interval-between-fetch=0s --tron-endpoints=http://my.tron.endpoint --tron-api-key=xxxxxxxxx
```

```
firetron fetch-evm 0 --tron-evm-endpoints=https://provider/jsonrpc --block-fetch-batch-size=1 --interval-between-fetch=1s --tron-endpoints=http://tron.grpc.endpoint:12345 --state-dir=/persistent/path --tron-api-key=xxxxxxx
```
