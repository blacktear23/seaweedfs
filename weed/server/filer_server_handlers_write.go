package weed_server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/chrislusf/seaweedfs/weed/s3api/s3_constants"

	"github.com/chrislusf/seaweedfs/weed/glog"
	"github.com/chrislusf/seaweedfs/weed/operation"
	"github.com/chrislusf/seaweedfs/weed/pb/filer_pb"
	"github.com/chrislusf/seaweedfs/weed/security"
	"github.com/chrislusf/seaweedfs/weed/stats"
	"github.com/chrislusf/seaweedfs/weed/storage/needle"
	"github.com/chrislusf/seaweedfs/weed/util"
)

var (
	OS_UID = uint32(os.Getuid())
	OS_GID = uint32(os.Getgid())

	ErrReadOnly = errors.New("read only")
)

type FilerPostResult struct {
	Name  string `json:"name,omitempty"`
	Size  int64  `json:"size,omitempty"`
	Error string `json:"error,omitempty"`
}

func (fs *FilerServer) assignNewFileInfo(so *operation.StorageOption) (fileId, urlLocation string, auth security.EncodedJwt, err error) {

	stats.FilerRequestCounter.WithLabelValues("assign").Inc()
	start := time.Now()
	defer func() { stats.FilerRequestHistogram.WithLabelValues("assign").Observe(time.Since(start).Seconds()) }()

	ar, altRequest := so.ToAssignRequests(1)

	assignResult, ae := operation.Assign(fs.filer.GetMaster, fs.grpcDialOption, ar, altRequest)
	if ae != nil {
		glog.Errorf("failing to assign a file id: %v", ae)
		err = ae
		return
	}
	fileId = assignResult.Fid
	urlLocation = "http://" + assignResult.Url + "/" + assignResult.Fid
	if so.Fsync {
		urlLocation += "?fsync=true"
	}
	auth = assignResult.Auth
	return
}

func (fs *FilerServer) PostHandler(w http.ResponseWriter, r *http.Request, contentLength int64) {

	ctx := context.Background()

	destination := r.RequestURI
	if finalDestination := r.Header.Get(s3_constants.SeaweedStorageDestinationHeader); finalDestination != "" {
		destination = finalDestination
	}

	query := r.URL.Query()
	so, err := fs.detectStorageOption0(destination,
		query.Get("collection"),
		query.Get("replication"),
		query.Get("ttl"),
		query.Get("disk"),
		query.Get("fsync"),
		query.Get("dataCenter"),
		query.Get("rack"),
		query.Get("dataNode"),
	)
	if err != nil {
		if err == ErrReadOnly {
			w.WriteHeader(http.StatusInsufficientStorage)
		} else {
			glog.V(1).Infoln("post", r.RequestURI, ":", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	if query.Has("mv.from") {
		fs.move(ctx, w, r, so)
	} else if query.Has("source_url") {
		fs.uploadFromSourceURL(ctx, w, r, so)
	} else {
		fs.autoChunk(ctx, w, r, contentLength, so)
	}

	util.CloseRequest(r)

}

func (fs *FilerServer) uploadFromSourceURL(ctx context.Context, w http.ResponseWriter, r *http.Request, so *operation.StorageOption) {
	sourceUrl := r.URL.Query().Get("source_url")
	srcUrl, err := url.ParseRequestURI(sourceUrl)
	if err != nil {
		writeJsonError(w, r, http.StatusBadRequest, err)
		return
	}
	if srcUrl.Scheme != "http" && srcUrl.Scheme != "https" {
		writeJsonError(w, r, http.StatusBadRequest, fmt.Errorf("Invalid protocol: %s", srcUrl.Scheme))
		return
	}

	resp, err := http.Get(srcUrl.String())
	if err != nil {
		writeJsonError(w, r, http.StatusBadRequest, err)
		return
	}
	defer resp.Body.Close()
	// Only HTTP 200 OK is valid response
	if resp.StatusCode != 200 {
		writeJsonError(w, r, http.StatusBadRequest, fmt.Errorf("Bad source URL status code: %d", resp.StatusCode))
		return
	}

	contentLength := resp.ContentLength
	contentType := resp.Header.Get("Content-Type")
	fs.autoChunkWithOtherStream(ctx, w, r, resp.Body, contentLength, contentType, so)

	util.CloseRequest(r)
}

func (fs *FilerServer) move(ctx context.Context, w http.ResponseWriter, r *http.Request, so *operation.StorageOption) {
	src := r.URL.Query().Get("mv.from")
	dst := r.URL.Path

	glog.V(2).Infof("FilerServer.move %v to %v", src, dst)

	var err error
	if src, err = clearName(src); err != nil {
		writeJsonError(w, r, http.StatusBadRequest, err)
		return
	}
	if dst, err = clearName(dst); err != nil {
		writeJsonError(w, r, http.StatusBadRequest, err)
		return
	}
	src = strings.TrimRight(src, "/")
	if src == "" {
		err = fmt.Errorf("invalid source '/'")
		writeJsonError(w, r, http.StatusBadRequest, err)
		return
	}

	srcPath := util.FullPath(src)
	dstPath := util.FullPath(dst)
	srcEntry, err := fs.filer.FindEntry(ctx, srcPath)
	if err != nil {
		err = fmt.Errorf("failed to get src entry '%s', err: %s", src, err)
		writeJsonError(w, r, http.StatusBadRequest, err)
		return
	}

	oldDir, oldName := srcPath.DirAndName()
	newDir, newName := dstPath.DirAndName()
	newName = util.Nvl(newName, oldName)

	dstEntry, err := fs.filer.FindEntry(ctx, util.FullPath(strings.TrimRight(dst, "/")))
	if err != nil && err != filer_pb.ErrNotFound {
		err = fmt.Errorf("failed to get dst entry '%s', err: %s", dst, err)
		writeJsonError(w, r, http.StatusInternalServerError, err)
		return
	}
	if err == nil && !dstEntry.IsDirectory() && srcEntry.IsDirectory() {
		err = fmt.Errorf("move: cannot overwrite non-directory '%s' with directory '%s'", dst, src)
		writeJsonError(w, r, http.StatusBadRequest, err)
		return
	}

	_, err = fs.AtomicRenameEntry(ctx, &filer_pb.AtomicRenameEntryRequest{
		OldDirectory: oldDir,
		OldName:      oldName,
		NewDirectory: newDir,
		NewName:      newName,
	})
	if err != nil {
		err = fmt.Errorf("failed to move entry from '%s' to '%s', err: %s", src, dst, err)
		writeJsonError(w, r, http.StatusBadRequest, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// curl -X DELETE http://localhost:8888/path/to
// curl -X DELETE http://localhost:8888/path/to?recursive=true
// curl -X DELETE http://localhost:8888/path/to?recursive=true&ignoreRecursiveError=true
// curl -X DELETE http://localhost:8888/path/to?recursive=true&skipChunkDeletion=true
func (fs *FilerServer) DeleteHandler(w http.ResponseWriter, r *http.Request) {

	isRecursive := r.FormValue("recursive") == "true"
	if !isRecursive && fs.option.recursiveDelete {
		if r.FormValue("recursive") != "false" {
			isRecursive = true
		}
	}
	ignoreRecursiveError := r.FormValue("ignoreRecursiveError") == "true"
	skipChunkDeletion := r.FormValue("skipChunkDeletion") == "true"

	objectPath := r.URL.Path
	if len(r.URL.Path) > 1 && strings.HasSuffix(objectPath, "/") {
		objectPath = objectPath[0 : len(objectPath)-1]
	}

	err := fs.filer.DeleteEntryMetaAndData(context.Background(), util.FullPath(objectPath), isRecursive, ignoreRecursiveError, !skipChunkDeletion, false, nil)
	if err != nil {
		glog.V(1).Infoln("deleting", objectPath, ":", err.Error())
		httpStatus := http.StatusInternalServerError
		if err == filer_pb.ErrNotFound {
			httpStatus = http.StatusNoContent
		}
		writeJsonError(w, r, httpStatus, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (fs *FilerServer) detectStorageOption(requestURI, qCollection, qReplication string, ttlSeconds int32, diskType, dataCenter, rack, dataNode string) (*operation.StorageOption, error) {

	rule := fs.filer.FilerConf.MatchStorageRule(requestURI)

	if rule.ReadOnly {
		return nil, ErrReadOnly
	}

	// required by buckets folder
	bucketDefaultCollection := ""
	if strings.HasPrefix(requestURI, fs.filer.DirBucketsPath+"/") {
		bucketDefaultCollection = fs.filer.DetectBucket(util.FullPath(requestURI))
	}

	if ttlSeconds == 0 {
		ttl, err := needle.ReadTTL(rule.GetTtl())
		if err != nil {
			glog.Errorf("fail to parse %s ttl setting %s: %v", rule.LocationPrefix, rule.Ttl, err)
		}
		ttlSeconds = int32(ttl.Minutes()) * 60
	}

	return &operation.StorageOption{
		Replication:       util.Nvl(qReplication, rule.Replication, fs.option.DefaultReplication),
		Collection:        util.Nvl(qCollection, rule.Collection, bucketDefaultCollection, fs.option.Collection),
		DataCenter:        util.Nvl(dataCenter, rule.DataCenter, fs.option.DataCenter),
		Rack:              util.Nvl(rack, rule.Rack, fs.option.Rack),
		DataNode:          util.Nvl(dataNode, rule.DataNode, fs.option.DataNode),
		TtlSeconds:        ttlSeconds,
		DiskType:          util.Nvl(diskType, rule.DiskType),
		Fsync:             rule.Fsync,
		VolumeGrowthCount: rule.VolumeGrowthCount,
	}, nil
}

func (fs *FilerServer) detectStorageOption0(requestURI, qCollection, qReplication string, qTtl string, diskType string, fsync string, dataCenter, rack, dataNode string) (*operation.StorageOption, error) {

	ttl, err := needle.ReadTTL(qTtl)
	if err != nil {
		glog.Errorf("fail to parse ttl %s: %v", qTtl, err)
	}

	so, err := fs.detectStorageOption(requestURI, qCollection, qReplication, int32(ttl.Minutes())*60, diskType, dataCenter, rack, dataNode)
	if so != nil {
		if fsync == "false" {
			so.Fsync = false
		} else if fsync == "true" {
			so.Fsync = true
		}
	}

	return so, err
}
