package gateway

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestAnthropicTextCallsOverlappingBlocks(t *testing.T) {
	for _, next := range []string{"text", "tool_use"} {
		t.Run(next, func(t *testing.T) {
			block, delta := `{"type":"text","text":""}`, `{"type":"text_delta","text":"checking"}`
			if next == "tool_use" {
				block, delta = `{"type":"tool_use","id":"toolu_test","name":"probe","input":{}}`, `{"type":"input_json_delta","partial_json":"{}"}`
			}
			frames := []struct{ event, fields string }{
				{"content_block_start", `"index":1,"content_block":{"type":"text","text":""}`},
				{"content_block_start", `"index":2,"content_block":` + block},
				{"content_block_delta", `"index":2,"delta":` + delta},
				{"content_block_stop", `"index":1`},
				{"content_block_stop", `"index":2`},
			}
			filter := &anthropicTextCalls{names: map[string]bool{"probe": true}, sse: true}
			var input, output strings.Builder
			for _, frame := range frames {
				event := fmt.Sprintf(`event: %s
data: {"type":%q,%s}

`, frame.event, frame.event, frame.fields)
				input.WriteString(event)
				output.Write(filter.write([]byte(event)))
			}
			output.Write(filter.flush())
			if want, got := readAnthropicSSE(t, input.String()), readAnthropicSSE(t, output.String()); !reflect.DeepEqual(want, got) {
				t.Fatalf("overlapping blocks changed in transit:\n%s", output.String())
			}
		})
	}
}
