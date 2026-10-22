package brain

import "testing"

// gbrain reads the page from stdin unless --content carries it inline ("omit
// --content, or put it last, to read stdin"). The wrapper must only buffer
// stdin when gbrain would read it, so `put --content "..."` from a terminal
// cannot block waiting for an EOF that never comes.
func TestPutNeedsStdin(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"put", "p/s"}, true},
		{[]string{"--quiet", "put", "p/s"}, true},
		{[]string{"put", "p/s", "--content", "inline page text"}, false},
		{[]string{"put", "p/s", "--content=inline page text"}, false},
		{[]string{"put", "p/s", "--content"}, true},
	}
	for _, c := range cases {
		if got := putNeedsStdin(c.args); got != c.want {
			t.Errorf("putNeedsStdin(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}
