package bot

import (
	"bufio"
	"os"
)

func TailLastNLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Simple approach: scan all lines, keep last n.
	// errors.log is expected to stay small-ish; if it grows, we can optimize later.
	buf := make([]string, 0, n)
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if len(buf) < n {
			buf = append(buf, line)
			continue
		}
		copy(buf, buf[1:])
		buf[n-1] = line
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return buf, nil
}
