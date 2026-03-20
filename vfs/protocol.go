package vfs

// Wire protocol types for the VFS proxy. Both the Go server and the Rust
// client serialize/deserialize these as msgpack over a Unix domain socket.
//
// Each request is a framed message: 4-byte big-endian length prefix followed
// by the msgpack payload. Responses use the same framing.

// Op tags for request dispatch.
const (
	OpReadlink        = 1
	OpReadDir         = 2
	OpCreateDir       = 3
	OpRemoveDir       = 4
	OpRename          = 5
	OpMetadata        = 6
	OpSymlinkMetadata = 7
	OpRemoveFile      = 8
	OpOpen            = 9
	OpMount           = 10

	// File handle operations.
	OpFileRead   = 20
	OpFileWrite  = 21
	OpFileSeek   = 22
	OpFileFlush  = 23
	OpFileClose  = 24
	OpFileSetLen = 25
)

// Error codes returned in responses.
const (
	ErrOK            = 0
	ErrNotFound      = 1
	ErrAlreadyExists = 2
	ErrPermission    = 3
	ErrNotDir        = 4
	ErrNotFile       = 5
	ErrNotEmpty      = 6
	ErrIO            = 7
	ErrInvalidInput  = 8
	ErrUnsupported   = 9
	ErrUnknown       = 10
)

// Request is the top-level wire request.
type Request struct {
	Op   int    `msgpack:"op"`
	Path string `msgpack:"path,omitempty"`

	// Rename
	ToPath string `msgpack:"to_path,omitempty"`

	// Open options
	OpenOpts *OpenOpts `msgpack:"open_opts,omitempty"`

	// File handle operations
	Handle uint64 `msgpack:"handle,omitempty"`
	Data   []byte `msgpack:"data,omitempty"`
	Len    int64  `msgpack:"len,omitempty"`

	// Seek
	SeekFrom int   `msgpack:"seek_from,omitempty"` // 0=Start, 1=Current, 2=End
	SeekPos  int64 `msgpack:"seek_pos,omitempty"`
}

type OpenOpts struct {
	Read      bool `msgpack:"read"`
	Write     bool `msgpack:"write"`
	Create    bool `msgpack:"create"`
	CreateNew bool `msgpack:"create_new"`
	Append    bool `msgpack:"append"`
	Truncate  bool `msgpack:"truncate"`
}

// Response is the top-level wire response.
type Response struct {
	Err int `msgpack:"err"`

	// readlink
	Target string `msgpack:"target,omitempty"`

	// read_dir
	Entries []DirEntryWire `msgpack:"entries,omitempty"`

	// metadata / symlink_metadata
	Meta *MetadataWire `msgpack:"meta,omitempty"`

	// open -> handle
	Handle uint64 `msgpack:"handle,omitempty"`

	// file read
	Data []byte `msgpack:"data,omitempty"`

	// file seek -> new position
	Pos int64 `msgpack:"pos,omitempty"`

	// file read -> bytes read
	N int `msgpack:"n,omitempty"`
}

type DirEntryWire struct {
	Name string       `msgpack:"name"`
	Meta MetadataWire `msgpack:"meta"`
}

type MetadataWire struct {
	IsDir    bool   `msgpack:"is_dir"`
	IsFile   bool   `msgpack:"is_file"`
	Len      uint64 `msgpack:"len"`
	Accessed uint64 `msgpack:"accessed"`
	Created  uint64 `msgpack:"created"`
	Modified uint64 `msgpack:"modified"`
}
