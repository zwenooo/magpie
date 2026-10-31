package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

// Bulk space scans must stop at every non-space byte, including JSON's other
// whitespace and bytes JSON forbids. Offsets exercise unaligned block edges.
func TestJSONSpaceBlocksKeepBoundaries(t *testing.T) {
	for _, n := range []int{0, 1, 7, 8, 9, 255, 256, 257, 1023, 1024, 1025, 2047, 2048, 2049, 8192} {
		for _, offset := range []int{0, 1, 7, 15} {
			for _, delimiter := range []byte{'{', '\t', '\n', '\r', '\v', 0, 0xff} {
				input := append(bytes.Repeat([]byte{'x'}, offset), bytes.Repeat([]byte{' '}, n)...)
				input = append(input, delimiter, '!')
				input = append(input, bytes.Repeat([]byte{' '}, 2048)...)
				want := offset + n
				if delimiter == '\t' || delimiter == '\n' || delimiter == '\r' {
					want++
				}
				if got := skipJSONSpace(input, offset); got != want {
					t.Fatalf("n=%d offset=%d delimiter=%x: skipped to %d, want %d", n, offset, delimiter, got, want)
				}
			}
		}
		pad := bytes.Repeat([]byte{' '}, n)
		for _, content := range [][]byte{nil, []byte("{}"), []byte("\v{}\x00"), []byte("\xff")} {
			input := append(append(append([]byte{}, pad...), content...), pad...)
			if got := trimJSON(input); !bytes.Equal(got, content) {
				t.Fatalf("n=%d: trimmed %x, want %x", n, got, content)
			}
		}
	}
}

func TestJSONSpaceBlocksKeepMixedWhitespaceAndValidation(t *testing.T) {
	pad := append(bytes.Repeat([]byte{' '}, 2051), '\t', '\r', '\n')
	pad = append(pad, bytes.Repeat([]byte{' '}, 2053)...)
	for _, payload := range []string{`{"value":" a b "}`, `{"value":"unterminated}`, `{} {}`, "{}\v", "\x00{}"} {
		t.Run(fmt.Sprintf("%q", payload), func(t *testing.T) {
			input := append(append(append([]byte{}, pad...), payload...), pad...)
			if got := trimJSON(input); string(got) != payload {
				t.Fatalf("trimmed %q, want %q", got, payload)
			}
			if got, want := jsonValid(input), json.Valid(input); got != want {
				t.Fatalf("validation = %v, encoding/json = %v", got, want)
			}
			fjCompare(t, input)
		})
	}
}

// A newline at the padding's edge ends one bulk run, and the next resumes
// after it: everything up to the payload is still skipped, from either end.
func TestJSONSpaceBlocksResumeAfterOtherWhitespace(t *testing.T) {
	pad := bytes.Repeat([]byte{' '}, 4099)
	for _, edge := range []string{"\n", " \n", "\r\n", "\t \t", "\n\n\n"} {
		input := append(append([]byte(edge), pad...), edge...)
		input = append(append(input, "{} {}"...), edge...)
		input = append(append(input, pad...), edge...)
		if got := trimJSON(input); string(got) != "{} {}" {
			t.Fatalf("edge %q: trimmed %q", edge, got)
		}
		if got, want := skipJSONSpace(input, 0), 2*len(edge)+len(pad); got != want {
			t.Fatalf("edge %q: skipped to %d, want %d", edge, got, want)
		}
	}
}

func BenchmarkJSONPadding64MiB(b *testing.B) {
	pad := bytes.Repeat([]byte{' '}, 64<<20)
	leading := append(bytes.Clone(pad), '{')
	trailing := append([]byte{'{'}, pad...)
	// TestRequestTailAfterOldLimit's body: padding, then a client's " \n".
	newlineLeading := append(append([]byte("\n"), pad...), '{')
	newlineTrailing := append(append([]byte{'{'}, pad...), " \n"...)
	for _, tc := range []struct {
		name string
		body []byte
	}{{"leading", leading}, {"trailing", trailing}, {"newline-leading", newlineLeading}, {"newline-trailing", newlineTrailing}} {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(pad)))
			b.ReportAllocs()
			for range b.N {
				if len(trimJSON(tc.body)) != 1 {
					b.Fatal("payload was not preserved")
				}
			}
		})
	}
}
