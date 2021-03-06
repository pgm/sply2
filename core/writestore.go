package core

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
)

type WriteableStore interface {
	NewWriteRef() (WritableRef, error)
	NewFile() (string, error)
}

type WritableStoreImp struct {
	path string
}

type WritableRefImp struct {
	filename string
	offset   int64
}

func NewWritableRefImp(name string) WritableRef {
	return &WritableRefImp{name, 0}
}

func (w *WritableRefImp) Seek(offset int64, whence int) (int64, error) {
	if whence != 0 {
		panic("unimp")
	}
	w.offset = offset
	return w.offset, nil
}

func (w *WritableRefImp) Read(ctx context.Context, dest []byte) (int, error) {
	f, err := os.OpenFile(w.filename, os.O_RDONLY, 0755)
	if err != nil {
		return 0, err
	}

	defer f.Close()
	fmt.Printf("Reading from %s:%d\n", w.filename, w.offset)
	_, err = f.Seek(w.offset, 0)
	if err != nil {
		return 0, err
	}

	n, err := f.Read(dest)
	if err != nil {
		return 0, err
	}
	w.offset += int64(n)
	return n, err
}

func (w *WritableRefImp) Write(buffer []byte) (int, error) {
	log.Printf("Writing %d bytes to %s:%d", len(buffer), w.filename, w.offset)
	f, err := os.OpenFile(w.filename, os.O_RDWR, 0755)
	if err != nil {
		return 0, err
	}

	defer f.Close()

	n, err := f.Seek(w.offset, 0)
	if err != nil {
		return 0, err
	}
	w.offset += n

	return f.Write(buffer)
}

func (w *WritableRefImp) Release() {

}

func (w *WritableStoreImp) NewWriteRef() (WritableRef, error) {
	name, err := w.NewFile()
	if err != nil {
		return nil, err
	}
	return NewWritableRefImp(name), nil
}

func (w *WritableStoreImp) NewFile() (string, error) {
	f, err := ioutil.TempFile(w.path, "dat")
	if err != nil {
		return "", err
	}
	name := f.Name()
	f.Close()
	return name, nil
}

func NewWritableStore(path string) WriteableStore {
	return &WritableStoreImp{path}
}
