/*
 * Copyright 2018-2019, CS Systemes d'Information, http://www.c-s.fr
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

package listeners

import (
	"context"
	"fmt"

	pb "github.com/CS-SI/SafeScale/lib"
	"github.com/CS-SI/SafeScale/lib/server/handlers"
	"github.com/CS-SI/SafeScale/lib/server/utils"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// ProcessManagerHandler ...
var ProcessManagerHandler = handlers.NewProcessManagerHandler

// ProcessManagerListener service server gRPC
type ProcessManagerListener struct{}

// Stop specified process
func (s *ProcessManagerListener) Stop(ctx context.Context, in *pb.ProcessDefinition) (*google_protobuf.Empty, error) {
	log.Infof("Stop process called")

	ctx, cancelFunc := context.WithCancel(ctx)

	if err := utils.ProcessRegister(ctx, cancelFunc, "Stop process "+in.Uuid); err == nil {
		defer utils.ProcessDeregister(ctx)
	}

	tenant := GetCurrentTenant()
	if tenant == nil {
		log.Info("Can't stop process: no tenant set")
		return nil, grpc.Errorf(codes.FailedPrecondition, "Can't stop process: no tenant set")
	}

	handler := ProcessManagerHandler(tenant.Service)
	handler.Stop(ctx, in.Uuid)

	return &google_protobuf.Empty{}, nil
}

// List running process
func (s *ProcessManagerListener) List(ctx context.Context, in *google_protobuf.Empty) (*pb.ProcessList, error) {
	log.Infof("List process called")

	ctx, cancelFunc := context.WithCancel(ctx)

	if err := utils.ProcessRegister(ctx, cancelFunc, "List Processes"); err == nil {
		defer utils.ProcessDeregister(ctx)
	}

	tenant := GetCurrentTenant()
	if tenant == nil {
		log.Info("Can't list process : no tenant set")
		return nil, grpc.Errorf(codes.FailedPrecondition, "Can't list process: no tenant set")
	}

	handler := ProcessManagerHandler(tenant.Service)
	processMap, err := handler.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed to list Process %s", err.Error())
	}
	var pbProcessList []*pb.ProcessDefinition
	for uuid, info := range processMap {
		pbProcessList = append(pbProcessList, &pb.ProcessDefinition{Uuid: uuid, Info: info})
	}

	return &pb.ProcessList{List: pbProcessList}, nil
}