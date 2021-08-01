package weed_server

import (
	"context"
	"fmt"
	"github.com/chrislusf/seaweedfs/weed/pb/filer_pb"
	"github.com/chrislusf/seaweedfs/weed/pb/volume_server_pb"
	"github.com/chrislusf/seaweedfs/weed/remote_storage"
	"github.com/chrislusf/seaweedfs/weed/storage/needle"
	"github.com/chrislusf/seaweedfs/weed/storage/types"
)

func (vs *VolumeServer) ReadNeedleBlob(ctx context.Context, req *volume_server_pb.ReadNeedleBlobRequest) (resp *volume_server_pb.ReadNeedleBlobResponse, err error) {
	resp = &volume_server_pb.ReadNeedleBlobResponse{}
	v := vs.store.GetVolume(needle.VolumeId(req.VolumeId))
	if v == nil {
		return nil, fmt.Errorf("not found volume id %d", req.VolumeId)
	}

	resp.NeedleBlob, err = v.ReadNeedleBlob(req.Offset, types.Size(req.Size))
	if err != nil {
		return nil, fmt.Errorf("read needle blob offset %d size %d: %v", req.Offset, req.Size, err)
	}

	return resp, nil
}

func (vs *VolumeServer) WriteNeedleBlob(ctx context.Context, req *volume_server_pb.WriteNeedleBlobRequest) (resp *volume_server_pb.WriteNeedleBlobResponse, err error) {
	resp = &volume_server_pb.WriteNeedleBlobResponse{}
	v := vs.store.GetVolume(needle.VolumeId(req.VolumeId))
	if v == nil {
		return nil, fmt.Errorf("not found volume id %d", req.VolumeId)
	}

	if err = v.WriteNeedleBlob(types.NeedleId(req.NeedleId), req.NeedleBlob, types.Size(req.Size)); err != nil {
		return nil, fmt.Errorf("write blob needle %d size %d: %v", req.NeedleId, req.Size, err)
	}

	return resp, nil
}

func (vs *VolumeServer) FetchAndWriteNeedle(ctx context.Context, req *volume_server_pb.FetchAndWriteNeedleRequest) (resp *volume_server_pb.FetchAndWriteNeedleResponse, err error) {
	resp = &volume_server_pb.FetchAndWriteNeedleResponse{}
	v := vs.store.GetVolume(needle.VolumeId(req.VolumeId))
	if v == nil {
		return nil, fmt.Errorf("not found volume id %d", req.VolumeId)
	}

	remoteConf := &filer_pb.RemoteConf{
		Type: req.RemoteType,
		Name: req.RemoteName,
		S3AccessKey: req.S3AccessKey,
		S3SecretKey: req.S3SecretKey,
		S3Region: req.S3Region,
		S3Endpoint: req.S3Endpoint,
	}

	client, getClientErr := remote_storage.GetRemoteStorage(remoteConf)
	if getClientErr != nil  {
		return nil, fmt.Errorf("get remote client: %v", getClientErr)
	}

	remoteStorageLocation := &filer_pb.RemoteStorageLocation{
		Name:   req.RemoteName,
		Bucket: req.RemoteBucket,
		Path:   req.RemoteKey,
	}
	data, ReadRemoteErr := client.ReadFile(remoteStorageLocation, req.Offset, req.Size)
	if ReadRemoteErr != nil {
		return nil, fmt.Errorf("read from remote %+v: %v", remoteStorageLocation, ReadRemoteErr)
	}

	if err = v.WriteNeedleBlob(types.NeedleId(req.NeedleId), data, types.Size(req.Size)); err != nil {
		return nil, fmt.Errorf("write blob needle %d size %d: %v", req.NeedleId, req.Size, err)
	}

	return resp, nil
}
