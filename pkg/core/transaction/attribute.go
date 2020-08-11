package transaction

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/nspcc-dev/neo-go/pkg/io"
)

// Attribute represents a Transaction attribute.
type Attribute struct {
	Usage AttrUsage
	Data  []byte
}

// attrJSON is used for JSON I/O of Attribute.
type attrJSON struct {
	Usage string `json:"usage"`
	Data  string `json:"data"`
}

// DecodeBinary implements Serializable interface.
func (attr *Attribute) DecodeBinary(br *io.BinReader) {
	attr.Usage = AttrUsage(br.ReadB())

	var datasize uint64
	/**

	switch attr.Usage {
	default:
		br.Err = fmt.Errorf("failed decoding TX attribute usage: 0x%2x", int(attr.Usage))
		return
	}
	*/
	attr.Data = make([]byte, datasize)
	br.ReadBytes(attr.Data)
}

// EncodeBinary implements Serializable interface.
func (attr *Attribute) EncodeBinary(bw *io.BinWriter) {
	bw.WriteB(byte(attr.Usage))
	switch attr.Usage {
	default:
		bw.Err = fmt.Errorf("failed encoding TX attribute usage: 0x%2x", attr.Usage)
	}
}

// MarshalJSON implements the json Marshaller interface.
func (attr *Attribute) MarshalJSON() ([]byte, error) {
	return json.Marshal(attrJSON{
		Usage: "", // attr.Usage.String() when we're to have some real attributes
		Data:  base64.StdEncoding.EncodeToString(attr.Data),
	})
}

// UnmarshalJSON implements the json.Unmarshaller interface.
func (attr *Attribute) UnmarshalJSON(data []byte) error {
	aj := new(attrJSON)
	err := json.Unmarshal(data, aj)
	if err != nil {
		return err
	}
	binData, err := base64.StdEncoding.DecodeString(aj.Data)
	if err != nil {
		return err
	}
	/**
	switch aj.Usage {
	default:
		return errors.New("wrong Usage")

	}
	*/
	attr.Data = binData
	return nil
}
