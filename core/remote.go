package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"time"
)

type RemoteFile struct {
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time

	// Fields for Remote GCS
	Bucket     string
	Key        string
	Generation int64
}

type RemoteRefFactory interface {
	GetRef(ctx context.Context, node *NodeRepr) (RemoteRef, error)
	Push(ctx context.Context, BID BlockID, rr io.Reader) error
	SetLease(ctx context.Context, name string, expiry time.Time, BID BlockID) error
	SetRoot(ctx context.Context, name string, BID BlockID) error
	GetRoot(ctx context.Context, name string) (BlockID, error)
	GetChildNodes(ctx context.Context, node *NodeRepr) ([]*RemoteFile, error)
}

type RemoteRefFactoryMem struct {
	leases  map[string]BlockID
	roots   map[string]BlockID
	objects map[string][]byte
	prefix  string
}

type MemCopy struct {
	buffer []byte
}

func (m *MemCopy) GetSize() int64 {
	return int64(len(m.buffer))
}

func (m *MemCopy) Copy(ctx context.Context, offset int64, len int64, writer io.Writer) error {
	n, err := writer.Write(m.buffer[offset : offset+len])
	if n != int(len) {
		panic(fmt.Sprintf("%d != %d", n, len))
	}
	if err != nil {
		panic(err)
	}
	return nil
}

func NewRemoteRefFactoryMem() *RemoteRefFactoryMem {
	return &RemoteRefFactoryMem{roots: make(map[string]BlockID),
		objects: make(map[string][]byte),
		prefix:  "blocks/",
		leases:  make(map[string]BlockID)}
}

func (r *RemoteRefFactoryMem) GetRef(ctx context.Context, node *NodeRepr) (RemoteRef, error) {
	key := GetBlockKey(r.prefix, node.BID)
	b, ok := r.objects[key]
	if !ok {
		panic("missing block")
	}
	return &MemCopy{b}, nil
}

func (r *RemoteRefFactoryMem) Push(ctx context.Context, BID BlockID, rr io.Reader) error {
	b, err := ioutil.ReadAll(rr)
	if err != nil {
		panic(err)
	}
	key := GetBlockKey(r.prefix, BID)
	r.objects[key] = b
	return nil
}

func (r *RemoteRefFactoryMem) SetLease(ctx context.Context, name string, expiry time.Time, BID BlockID) error {
	r.leases[name] = BID
	return nil
}

func (r *RemoteRefFactoryMem) SetRoot(ctx context.Context, name string, BID BlockID) error {
	r.roots[name] = BID
	return nil
}

func (r *RemoteRefFactoryMem) GetRoot(ctx context.Context, name string) (BlockID, error) {
	BID, ok := r.roots[name]
	if !ok {
		return BID, UndefinedRootErr
	}
	return BID, nil
}

func (r *RemoteRefFactoryMem) GetChildNodes(ctx context.Context, node *NodeRepr) ([]*RemoteFile, error) {
	prefix := node.Key + "/"
	dirs := make(map[string]bool)
	result := make([]*RemoteFile, 0, 100)
	now := time.Now()

	for key, value := range r.objects {
		if strings.HasPrefix(key, prefix) {
			name := key[len(prefix):]
			nextSlash := strings.Index(name, "/")
			if nextSlash >= 0 {
				name = name[:nextSlash]
				dirs[name] = true
			} else {
				rec := &RemoteFile{
					Name:       name,
					IsDir:      false,
					Size:       int64(len(value)),
					ModTime:    now,
					Bucket:     node.Bucket,
					Key:        key,
					Generation: 1}
				result = append(result, rec)
			}
		}
	}

	for name, _ := range dirs {
		rec := &RemoteFile{
			Name:       name,
			IsDir:      true,
			Size:       0,
			ModTime:    now,
			Bucket:     node.Bucket,
			Key:        node.Key + "/" + name,
			Generation: 0}
		result = append(result, rec)
	}

	return nil, nil
}

func GetBlockKey(CASKeyPrefix string, BID BlockID) string {
	return CASKeyPrefix + base64.URLEncoding.EncodeToString(BID[:])
}
