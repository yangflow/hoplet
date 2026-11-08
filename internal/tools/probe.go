package tools

import (
	"context"
	"encoding/json"

	"github.com/yangflow/hoplet/proto"
)

// ImageProbe is a set of one tool, image_probe, that answers with a PNG
// image of size bytes filled with a repeating byte pattern. It is test
// support for sending a large image through a real connection, and is not
// registered in the production tool table.
func ImageProbe(size int) *Set {
	return &Set{tools: []tool{{
		info:    proto.ToolInfo{Name: "image_probe", InputSchema: json.RawMessage(`{"type":"object"}`)},
		visible: func(proto.Session) bool { return true },
		call: func(context.Context, proto.Session, json.RawMessage) (any, error) {
			return imageResult{Value: map[string]int{"size": size}, Image: proto.ToolImage{MIME: "image/png", Data: ProbePattern(size)}}, nil
		},
	}}}
}

// ProbePattern is the bytes of an image_probe image of size bytes.
func ProbePattern(size int) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = byte(i*7 + i>>8)
	}
	return b
}
