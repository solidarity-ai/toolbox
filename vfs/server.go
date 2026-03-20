package vfs

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/vmihailenco/msgpack/v5"
)

// Server listens on a Unix domain socket and serves VFS operations backed by a
// MemFS. Multiple concurrent connections are supported. The caller owns the
// lifecycle.
type Server struct {
	fs       *MemFS
	listener net.Listener

	// File handle tracking.
	handleMu   sync.Mutex
	nextHandle uint64
	handles    map[uint64]*memFileHandle

	done atomic.Bool
	wg   sync.WaitGroup
}

// NewServer creates a VFS server. Call Serve() to start accepting connections.
func NewServer(fs *MemFS, listener net.Listener) *Server {
	return &Server{
		fs:       fs,
		listener: listener,
		handles:  make(map[uint64]*memFileHandle),
	}
}

// Serve accepts connections until the listener is closed.
func (s *Server) Serve() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.done.Load() {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(conn)
		}()
	}
}

// Close stops accepting connections and waits for in-flight requests.
func (s *Server) Close() error {
	s.done.Store(true)
	err := s.listener.Close()
	s.wg.Wait()
	return err
}

// SocketPath returns the address of the listener.
func (s *Server) SocketPath() string {
	return s.listener.Addr().String()
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	for {
		req, err := readFrame[Request](conn)
		if err != nil {
			return // client disconnected or protocol error
		}
		resp := s.dispatch(req)
		if err := writeFrame(conn, resp); err != nil {
			return
		}
	}
}

func (s *Server) dispatch(req Request) Response {
	switch req.Op {
	case OpReadlink:
		target, err := s.fs.Readlink(req.Path)
		if err != nil {
			return errResp(err)
		}
		return Response{Target: target}

	case OpReadDir:
		entries, err := s.fs.ReadDir(req.Path)
		if err != nil {
			return errResp(err)
		}
		return Response{Entries: entries}

	case OpCreateDir:
		if err := s.fs.CreateDir(req.Path); err != nil {
			return errResp(err)
		}
		return Response{}

	case OpRemoveDir:
		if err := s.fs.RemoveDir(req.Path); err != nil {
			return errResp(err)
		}
		return Response{}

	case OpRename:
		if err := s.fs.Rename(req.Path, req.ToPath); err != nil {
			return errResp(err)
		}
		return Response{}

	case OpMetadata:
		meta, err := s.fs.Metadata(req.Path)
		if err != nil {
			return errResp(err)
		}
		return Response{Meta: &meta}

	case OpSymlinkMetadata:
		meta, err := s.fs.Metadata(req.Path) // same as metadata for now
		if err != nil {
			return errResp(err)
		}
		return Response{Meta: &meta}

	case OpRemoveFile:
		if err := s.fs.RemoveFile(req.Path); err != nil {
			return errResp(err)
		}
		return Response{}

	case OpOpen:
		opts := OpenOpts{}
		if req.OpenOpts != nil {
			opts = *req.OpenOpts
		}
		handle, err := s.fs.Open(req.Path, opts)
		if err != nil {
			return errResp(err)
		}
		s.handleMu.Lock()
		s.nextHandle++
		id := s.nextHandle
		s.handles[id] = handle
		s.handleMu.Unlock()
		return Response{Handle: id}

	case OpMount:
		return Response{Err: ErrUnsupported}

	case OpFileRead:
		h := s.getHandle(req.Handle)
		if h == nil {
			return Response{Err: ErrInvalidInput}
		}
		data, n, err := h.Read(int(req.Len))
		if err != nil {
			if err == io.EOF {
				return Response{Data: data, N: n}
			}
			return errResp(err)
		}
		return Response{Data: data, N: n}

	case OpFileWrite:
		h := s.getHandle(req.Handle)
		if h == nil {
			return Response{Err: ErrInvalidInput}
		}
		n, err := h.Write(req.Data)
		if err != nil {
			return errResp(err)
		}
		return Response{N: n}

	case OpFileSeek:
		h := s.getHandle(req.Handle)
		if h == nil {
			return Response{Err: ErrInvalidInput}
		}
		pos, err := h.Seek(req.SeekFrom, req.SeekPos)
		if err != nil {
			return errResp(err)
		}
		return Response{Pos: pos}

	case OpFileFlush:
		// No-op for in-memory FS.
		if s.getHandle(req.Handle) == nil {
			return Response{Err: ErrInvalidInput}
		}
		return Response{}

	case OpFileClose:
		s.handleMu.Lock()
		delete(s.handles, req.Handle)
		s.handleMu.Unlock()
		return Response{}

	case OpFileSetLen:
		h := s.getHandle(req.Handle)
		if h == nil {
			return Response{Err: ErrInvalidInput}
		}
		if err := h.SetLen(req.Len); err != nil {
			return errResp(err)
		}
		return Response{}

	default:
		return Response{Err: ErrUnsupported}
	}
}

func (s *Server) getHandle(id uint64) *memFileHandle {
	s.handleMu.Lock()
	defer s.handleMu.Unlock()
	return s.handles[id]
}

func errResp(err error) Response {
	msg := err.Error()
	code := ErrIO
	switch {
	case contains(msg, "not found"):
		code = ErrNotFound
	case contains(msg, "already exists"):
		code = ErrAlreadyExists
	case contains(msg, "not a directory"):
		code = ErrNotDir
	case contains(msg, "is a directory"):
		code = ErrNotFile
	case contains(msg, "not empty"):
		code = ErrNotEmpty
	case contains(msg, "not supported"):
		code = ErrUnsupported
	case contains(msg, "permission"):
		code = ErrPermission
	}
	return Response{Err: code}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsLinear(s, sub))
}

func containsLinear(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Frame I/O: 4-byte big-endian length prefix + msgpack payload.

func readFrame[T any](r io.Reader) (T, error) {
	var zero T
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return zero, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n > 16*1024*1024 { // 16 MB sanity limit
		return zero, fmt.Errorf("frame too large: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return zero, err
	}
	var msg T
	if err := msgpack.Unmarshal(buf, &msg); err != nil {
		return zero, fmt.Errorf("decode: %w", err)
	}
	return msg, nil
}

func writeFrame(w io.Writer, msg any) error {
	data, err := msgpack.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
