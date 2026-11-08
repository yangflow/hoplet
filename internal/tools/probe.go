package tools

import (
	"context"
	"encoding/json"

	"github.com/yangflow/hoplet/proto"
)

// ImageProbe is a set of one tool, image_probe, that answers with a PNG
// image of size bytes. Transport tests use it to send a large image through
// a real connection.
func ImageProbe(size int) *Set {
	return &Set{tools: []tool{{
		info:    proto.ToolInfo{Name: "image_probe", InputSchema: json.RawMessage(`{"type":"object"}`)},
		visible: func(proto.Session) bool { return true },
		call: func(context.Context, proto.Session, json.RawMessage) (any, error) {
			return imageResult{Value: map[string]int{"size": size}, Image: proto.ToolImage{MIME: "image/png", Data: make([]byte, size)}}, nil
		},
	}}}
}
