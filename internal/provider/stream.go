package provider

import (
	"bufio"
	"context"
	"io"
)

func scanLines(ctx context.Context, r io.Reader) (<-chan string, <-chan error) {
	lines := make(chan string)
	errs := make(chan error, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		errs <- scanner.Err()
	}()
	return lines, errs
}

func sendEvent(ctx context.Context, events chan<- Event, event Event) error {
	select {
	case events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
