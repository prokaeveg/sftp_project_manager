package transfer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type limitedProgressWriter struct {
	bytes.Buffer
	limit int
}

func (writer *limitedProgressWriter) Write(p []byte) (int, error) {
	if len(p) > writer.limit {
		p = p[:writer.limit]
	}
	return writer.Buffer.Write(p)
}

type zeroProgressWriter struct{}

func (zeroProgressWriter) Write([]byte) (int, error) { return 0, nil }

type errorProgressWriter struct{ err error }

func (writer errorProgressWriter) Write([]byte) (int, error) { return 0, writer.err }

func TestCopyContextReportsAcceptedBytesAcrossPartialWrites(t *testing.T) {
	payload := bytes.Repeat([]byte("transfer"), 20_000)
	writer := &limitedProgressWriter{limit: 733}
	var reported int64
	ctx := WithProgress(context.Background(), func(n int64) { reported += n })
	if err := copyContext(ctx, writer, bytes.NewReader(payload)); err != nil {
		t.Fatalf("copyContext() error = %v", err)
	}
	if !bytes.Equal(writer.Bytes(), payload) {
		t.Fatal("copyContext() did not finish writing payload after partial writes")
	}
	if reported != int64(len(payload)) {
		t.Fatalf("reported bytes = %d, want %d", reported, len(payload))
	}
}

func TestCopyContextReportsOnlyBytesAcceptedBeforeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var reported int64
	ctx = WithProgress(ctx, func(n int64) {
		reported += n
		cancel()
	})
	writer := &limitedProgressWriter{limit: 7}
	payload := []byte("longer than one write")
	err := copyContext(ctx, writer, bytes.NewReader(payload))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("copyContext() error = %v, want context.Canceled", err)
	}
	if reported != 7 || int64(writer.Len()) != reported {
		t.Fatalf("reported/written bytes = %d/%d, want 7/7", reported, writer.Len())
	}
}

func TestCopyContextPropagatesWriterErrorsAndShortWrites(t *testing.T) {
	wantErr := errors.New("disk full")
	if err := copyContext(WithProgress(context.Background(), func(int64) {}), errorProgressWriter{err: wantErr}, bytes.NewReader([]byte("data"))); !errors.Is(err, wantErr) {
		t.Fatalf("copyContext() error = %v, want wrapped writer error", err)
	}
	if err := copyContext(context.Background(), zeroProgressWriter{}, bytes.NewReader([]byte("data"))); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("copyContext() error = %v, want io.ErrShortWrite", err)
	}
}

func TestCopyContextDoesNotReportBytesWhenCancelledBeforeCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var reported int64
	ctx = WithProgress(ctx, func(n int64) { reported += n })
	writer := &bytes.Buffer{}
	err := copyContext(ctx, writer, bytes.NewReader([]byte("data")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("copyContext() error = %v, want context.Canceled", err)
	}
	if reported != 0 || writer.Len() != 0 {
		t.Fatalf("cancelled copy wrote/reported bytes = %d/%d, want 0/0", writer.Len(), reported)
	}
}
