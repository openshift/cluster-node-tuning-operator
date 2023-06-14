/*
 * Copyright 2023 Red Hat, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package deviceplugin

import (
	"context"
	"strconv"

	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/golang/glog"
)

const (
	initialDevicesQuantity = 15
	// the maximum pods per node are 256,
	// so this number should be more than enough
	devicesLimit = 1024
)

type message struct {
	requestedDevices int
}

type pluginImp struct {
	mutualCpus       *cpuset.CPUSet
	update           chan message
	allocatedDevices int
}

func (p pluginImp) ListAndWatch(empty *pluginapi.Empty, server pluginapi.DevicePlugin_ListAndWatchServer) error {
	var devID int
	devs := makeDevices(initialDevicesQuantity, devID)
	devID += len(devs)

	resp := &pluginapi.ListAndWatchResponse{Devices: devs}
	glog.V(4).Infof("ListAndWatch respond with: %+v", resp)
	if err := server.Send(resp); err != nil {
		return err
	}
	// never return, keep the connection open
	for {
		u := <-p.update
		p.allocatedDevices += u.requestedDevices
		if p.allocatedDevices > devicesLimit {
			glog.V(2).Infof("Warning: device limit has reached. can not populate more %q makeDevices", MutualCPUDeviceName)
			continue
		}
		// check if more makeDevices are needed
		if p.allocatedDevices >= len(devs) {
			newDevs := makeDevices(initialDevicesQuantity, devID)
			devID += len(newDevs)
			devs = append(devs, newDevs...)

			resp = &pluginapi.ListAndWatchResponse{Devices: devs}
			err := server.Send(resp)
			glog.V(4).Infof("ListAndWatch update respond with: %+v", resp)
			if err != nil {
				return err
			}
		}
	}
}

func (p pluginImp) Allocate(ctx context.Context, request *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	response := &pluginapi.AllocateResponse{}

	p.update <- message{requestedDevices: len(request.ContainerRequests)}

	glog.V(4).Infof("Allocate called with %+v", request)
	for range request.ContainerRequests {
		containerResponse := &pluginapi.ContainerAllocateResponse{
			Envs: map[string]string{"OPENSHIFT_MUTUAL_CPUS": p.mutualCpus.String()},
		}
		response.ContainerResponses = append(response.ContainerResponses, containerResponse)
	}
	return response, nil
}

func (p pluginImp) GetDevicePluginOptions(ctx context.Context, empty *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{}, nil
}

func (p pluginImp) GetPreferredAllocation(ctx context.Context, request *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method PreStartContainer not implemented")
}

func (p pluginImp) PreStartContainer(ctx context.Context, request *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method PreStartContainer not implemented")
}

func makeDevices(count, devID int) []*pluginapi.Device {
	var devs []*pluginapi.Device
	for i := 0; i < count; i++ {
		dev := &pluginapi.Device{
			ID:     strconv.Itoa(devID + i),
			Health: pluginapi.Healthy,
		}
		devs = append(devs, dev)
	}
	return devs
}
