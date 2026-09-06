package metacmd

import (
	"strings"
	"testing"

	"github.com/xo/usql/drivers/completer"
)

// TestCompleterKnowsAllCommands guards against meta commands missing from
// the completer's backslash command list: typing a command's full name minus
// its last rune at an empty prompt must offer that command. `\conns` was
// once forgotten, so this walks every registered command (including the
// `[...]` spelling variants) against the completer.
func TestCompleterKnowsAllCommands(t *testing.T) {
	comp := completer.NewDefaultCompleter()
	for _, sec := range descs {
		for _, d := range sec {
			for _, name := range d.Names() {
				cmd := strings.Fields(name)[0]
				typed := []rune("\\" + cmd[:len(cmd)-1])
				cands, length := comp.Do(typed, len(typed))
				if length != len(typed) {
					t.Fatalf("\\%s: completion length %d, want %d", cmd, length, len(typed))
				}
				found := false
				for _, c := range cands {
					if string(typed)+string(c) == "\\"+cmd {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("\\%s is registered as a meta command but is not offered by the completer", cmd)
				}
			}
		}
	}
}
