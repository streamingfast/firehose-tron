package pbtron

import (
	"time"
)

func (x *Block) GetFirehoseBlockID() string {
	return x.BlockId
}

func (x *Block) GetFirehoseBlockNumber() uint64 {
	return x.BlockHeader.RawData.Number
}

func (x *Block) GetFirehoseBlockParentID() string {
	return string(x.BlockHeader.RawData.ParentHash)
}

func (x *Block) GetFirehoseBlockParentNumber() uint64 {
	return x.BlockHeader.RawData.Number - 1
}

func (x *Block) GetFirehoseBlockTime() time.Time {
	return time.Unix(x.BlockHeader.RawData.Timestamp/1000, 0)
}
