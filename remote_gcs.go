package sply2

import (
	"encoding/base64"
	"encoding/gob"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"time"

	"google.golang.org/api/iterator"

	// Imports the Google Cloud Storage client package.

	"cloud.google.com/go/storage"
	"github.com/pgm/sply2/core"
	"golang.org/x/net/context"
)

type RemoteGCS struct {
	Bucket     *storage.BucketHandle // has ref to client
	Key        string
	Generation int64
	Size       int64
}

func (r *RemoteGCS) Copy(offset int64, len int64, writer io.Writer) error {
	ctx := context.Background()
	objHandle := r.Bucket.Object(r.Key)
	if r.Generation != 0 {
		objHandle = objHandle.If(storage.Conditions{GenerationMatch: r.Generation})
	}

	var reader io.ReadCloser
	var err error
	if offset != 0 || len != r.Size {
		reader, err = objHandle.NewRangeReader(ctx, offset, len)
	} else {
		reader, err = objHandle.NewReader(ctx)
	}
	if err != nil {
		return err
	}
	defer reader.Close()

	// TODO: more full implementation
	if offset != 0 || len != -1 {
		panic("needs fuller implementation. Assume copy should start at begining and copy entire file")
	}
	n, err := io.Copy(writer, reader)
	if err != nil {
		return err
	}

	if len >= 0 && n != len {
		return fmt.Errorf("Expected to copy to copy %d bytes but copied %d", len, n)
	}

	return nil
}

func (r *RemoteGCS) GetSize() int64 {
	return r.Size
}

func NewRemoteObject(client *storage.Client, bucketName string, key string) (*RemoteGCS, error) {
	ctx := context.Background()
	bucket := client.Bucket(bucketName)
	objHandle := bucket.Object(key)
	attr, err := objHandle.Attrs(ctx)
	if err != nil {
		return nil, err
	}

	return &RemoteGCS{Bucket: bucket, Key: key, Generation: attr.Generation, Size: attr.Size}, nil
}

type RemoteRefFactoryImp struct {
	Bucket         string
	RootKeyPrefix  string
	LeaseKeyPrefix string
	CASKeyPrefix   string
	GCSClient      *storage.Client
}

func (rrf *RemoteRefFactoryImp) GetChildNodes(node *core.NodeRepr) ([]*core.RemoteFile, error) {
	ctx := context.Background()
	b := rrf.GCSClient.Bucket(node.Bucket)
	it := b.Objects(ctx, &storage.Query{Delimiter: "/", Prefix: node.Key, Versions: false})
	result := make([]*core.RemoteFile, 0, 100)
	for {
		next, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var file *core.RemoteFile
		if next.Prefix != "" {
			// if we really want mtime we could get the mtime of the prefix because it's usually an empty object via
			// an explicit attr fetch of the prefix
			file = &core.RemoteFile{Name: next.Prefix[len(node.Key) : len(next.Prefix)-1],
				IsDir:  true,
				Bucket: node.Bucket,
				Key:    next.Prefix}
		} else {
			name := next.Name[len(node.Key):]
			if name == "" {
				continue
			}
			file = &core.RemoteFile{Name: name,
				IsDir:      false,
				Size:       next.Size,
				ModTime:    next.Updated,
				Bucket:     node.Bucket,
				Key:        next.Name,
				Generation: next.Generation}
		}

		result = append(result, file)
	}

	return result, nil
}

type Lease struct {
	Expiry time.Time
	BID    core.BlockID
}

func (rrf *RemoteRefFactoryImp) SetLease(name string, expiry time.Time, BID core.BlockID) error {
	ctx := context.Background()
	b := rrf.GCSClient.Bucket(rrf.Bucket)
	o := b.Object(rrf.LeaseKeyPrefix + name)
	w := o.NewWriter(ctx)
	defer w.Close()
	enc := gob.NewEncoder(w)
	err := enc.Encode(&Lease{Expiry: expiry, BID: BID})
	if err != nil {
		return err
	}
	return nil
}

func (rrf *RemoteRefFactoryImp) SetRoot(name string, BID core.BlockID) error {
	ctx := context.Background()
	b := rrf.GCSClient.Bucket(rrf.Bucket)
	o := b.Object(rrf.RootKeyPrefix + name)
	w := o.NewWriter(ctx)
	defer w.Close()

	BIDStr := base64.URLEncoding.EncodeToString(BID[:])
	_, err := w.Write([]byte(BIDStr))
	if err != nil {
		return err
	}

	return nil
}

func (rrf *RemoteRefFactoryImp) GetRoot(name string) (core.BlockID, error) {
	ctx := context.Background()
	b := rrf.GCSClient.Bucket(rrf.Bucket)
	o := b.Object(rrf.RootKeyPrefix + name)
	r, err := o.NewReader(ctx)
	if err != nil {
		return core.NABlock, err
	}
	defer r.Close()

	buffer, err := ioutil.ReadAll(r)
	if err != nil {
		return core.NABlock, err
	}

	var BID core.BlockID
	dbuffer := make([]byte, 1000)
	n, err := base64.URLEncoding.Decode(dbuffer, buffer)
	copy(BID[:], dbuffer[:n])
	if err != nil {
		return core.NABlock, err
	}
	return BID, nil
}

func (rrf *RemoteRefFactoryImp) GetGCSAttr(bucket string, key string) (*core.GCSAttrs, error) {
	if strings.HasSuffix(key, "/") || key == "" {
		// should we do some sanity check. For the moment, assuming bucket/key is always good
		return &core.GCSAttrs{IsDir: true}, nil
	}

	ctx := context.Background()
	b := rrf.GCSClient.Bucket(bucket)
	o := b.Object(key)
	attrs, err := o.Attrs(ctx)

	if err != nil {
		return nil, err
	}

	return &core.GCSAttrs{Generation: attrs.Generation, IsDir: false, ModTime: attrs.Updated, Size: attrs.Size}, nil
}

func (rrf *RemoteRefFactoryImp) Push(BID core.BlockID, rr io.Reader) error {
	ctx := context.Background()
	// upload only if generation == 0, which means this upload will fail if any object exists with the key
	// TODO: need to add a check for that case
	key := core.GetBlockKey(rrf.CASKeyPrefix, BID)
	// fmt.Println("bucket " + rrf.CASBucket + " " + key)
	CASBucketRef := rrf.GCSClient.Bucket(rrf.Bucket)
	objHandle := CASBucketRef.Object(key).If(storage.Conditions{DoesNotExist: true})
	writer := objHandle.NewWriter(ctx)
	defer writer.Close()

	_, err := io.Copy(writer, rr)
	if err != nil {
		return err
	}

	// fmt.Printf("Bytes copied: %d\n", n)

	return nil
}

func NewRemoteRefFactory(client *storage.Client, Bucket string, KeyPrefix string) *RemoteRefFactoryImp {
	if !strings.HasSuffix(KeyPrefix, "/") {
		panic("Prefix must end in /")
	}
	return &RemoteRefFactoryImp{GCSClient: client, Bucket: Bucket, CASKeyPrefix: KeyPrefix + "CAS/",
		RootKeyPrefix:  KeyPrefix + "root/",
		LeaseKeyPrefix: KeyPrefix + "lease/"}
}

func (rrf *RemoteRefFactoryImp) GetRef(node *core.NodeRepr) (core.RemoteRef, error) {
	var remote core.RemoteRef

	if node.URL != "" {
		remote = &RemoteURL{URL: node.URL, ETag: node.ETag, Length: node.Size}
	} else {
		if node.Key != "" {
			CASBucketRef := rrf.GCSClient.Bucket(node.Bucket)
			remote = &RemoteGCS{Bucket: CASBucketRef, Key: node.Key, Size: node.Size}
		} else {
			CASBucketRef := rrf.GCSClient.Bucket(rrf.Bucket)
			remote = &RemoteGCS{Bucket: CASBucketRef, Key: core.GetBlockKey(rrf.CASKeyPrefix, node.BID), Size: node.Size}
		}
	}
	// fmt.Printf("remote=%v\n", remote)

	return remote, nil
}
