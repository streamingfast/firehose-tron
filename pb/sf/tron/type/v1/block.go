package pbtron

import (
	"encoding/base64"
	"encoding/hex"
	"time"
)

func (x *Block) GetFirehoseBlockID() string {
	decoded, err := base64.StdEncoding.DecodeString(string(x.Id))
	if err != nil {
		return ""
	}
	return hex.EncodeToString(decoded)
}

func (x *Block) GetFirehoseBlockNumber() uint64 {
	return x.Header.Number
}

func (x *Block) GetFirehoseBlockParentID() string {
	decoded, err := base64.StdEncoding.DecodeString(string(x.Header.ParentHash))
	if err != nil {
		return ""
	}
	return hex.EncodeToString(decoded)
}

func (x *Block) GetFirehoseBlockParentNumber() uint64 {
	return x.Header.ParentNumber
}

func (x *Block) GetFirehoseBlockTime() time.Time {
	return time.Unix(x.Header.Timestamp/1000, 0)
}
