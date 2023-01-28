package backend

import (
	"os"
	"runtime"
	"time"

	"github.com/seaweedfs/seaweedfs/weed/glog"
	. "github.com/seaweedfs/seaweedfs/weed/storage/types"
)

var (
	_              BackendStorageFile = &DiskFile{}
	EnableIOUring                     = false
	IOUringEntries uint               = 256
)

const isMac = runtime.GOOS == "darwin"

type DiskFile struct {
	File         *os.File
	driver       IODriver
	fullFilePath string
	fileSize     int64
	modTime      time.Time
}

func NewDiskFile(f *os.File) *DiskFile {
	stat, err := f.Stat()
	if err != nil {
		glog.Fatalf("stat file %s: %v", f.Name(), err)
	}
	offset := stat.Size()
	if offset%NeedlePaddingSize != 0 {
		offset = offset + (NeedlePaddingSize - offset%NeedlePaddingSize)
	}

	driver := NewIODriver(f)

	return &DiskFile{
		fullFilePath: f.Name(),
		File:         f,
		driver:       driver,
		fileSize:     offset,
		modTime:      stat.ModTime(),
	}
}

func (df *DiskFile) ReadAt(p []byte, off int64) (n int, err error) {
	return df.driver.ReadAt(p, off)
}

func (df *DiskFile) WriteAt(p []byte, off int64) (n int, err error) {
	n, err = df.driver.WriteAt(p, off)
	if err == nil {
		waterMark := off + int64(n)
		if waterMark > df.fileSize {
			df.fileSize = waterMark
			df.modTime = time.Now()
		}
	}
	return
}

func (df *DiskFile) Write(p []byte) (n int, err error) {
	return df.WriteAt(p, df.fileSize)
}

func (df *DiskFile) Truncate(off int64) error {
	err := df.driver.Truncate(off)
	if err == nil {
		df.fileSize = off
		df.modTime = time.Now()
	}
	return err
}

func (df *DiskFile) Close() error {
	if err := df.Sync(); err != nil {
		return err
	}
	return df.driver.Close()
}

func (df *DiskFile) GetStat() (datSize int64, modTime time.Time, err error) {
	if df.File == nil {
		err = os.ErrInvalid
	}
	return df.fileSize, df.modTime, err
}

func (df *DiskFile) Name() string {
	return df.fullFilePath
}

func (df *DiskFile) Sync() error {
	if df.File == nil {
		return os.ErrInvalid
	}
	if isMac {
		return nil
	}
	return df.driver.Sync()
}
