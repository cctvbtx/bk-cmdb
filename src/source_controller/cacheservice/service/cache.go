/*
 * Tencent is pleased to support the open source community by making 蓝鲸 available.
 * Copyright (C) 2017-2018 THL A29 Limited, a Tencent company. All rights reserved.
 * Licensed under the MIT License (the "License"); you may not use this file except
 * in compliance with the License. You may obtain a copy of the License at
 * http://opensource.org/licenses/MIT
 * Unless required by applicable law or agreed to in writing, software distributed under
 * the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
 * either express or implied. See the License for the specific language governing permissions and
 * limitations under the License.
 */

package service

import (
	"strconv"

	"configcenter/src/common"
	"configcenter/src/common/blog"
	"configcenter/src/common/http/rest"
	"configcenter/src/common/metadata"
	"configcenter/src/source_controller/cacheservice/cache/topo_tree"
	"configcenter/src/storage/driver/redis"
)

func (s *cacheService) SearchTopologyTreeInCache(ctx *rest.Contexts) {
	opt := new(topo_tree.SearchOption)
	if err := ctx.DecodeInto(&opt); nil != err {
		ctx.RespAutoError(err)
		return
	}

	if err := opt.Validate(); err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommHTTPInputInvalid, "search topology tree, but request parameter is invalid: %v", err)
		return
	}

	topo, err := s.cacheSet.Topology.SearchTopologyTree(opt)
	if err != nil {
		if err == topo_tree.OverHeadError {
			ctx.RespWithError(err, common.SearchTopoTreeScanTooManyData, "search topology tree failed, err: %v", err)
			return
		}
		ctx.RespErrorCodeOnly(common.CCErrCommDBSelectFailed, "search topology tree failed, err: %v", err)
		return
	}
	ctx.RespEntity(topo)
}

func (s *cacheService) SearchHostWithInnerIPInCache(ctx *rest.Contexts) {
	opt := new(metadata.SearchHostWithInnerIPOption)
	if err := ctx.DecodeInto(&opt); nil != err {
		ctx.RespAutoError(err)
		return
	}
	host, err := s.cacheSet.Host.GetHostWithInnerIP(ctx.Kit.Ctx, opt)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommDBSelectFailed, "search host with inner ip in cache, but get host failed, err: %v", err)
		return
	}
	ctx.RespString(host)
}

func (s *cacheService) SearchHostWithHostIDInCache(ctx *rest.Contexts) {
	opt := new(metadata.SearchHostWithIDOption)
	if err := ctx.DecodeInto(&opt); nil != err {
		ctx.RespAutoError(err)
		return
	}

	host, err := s.cacheSet.Host.GetHostWithID(ctx.Kit.Ctx, opt)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommDBSelectFailed, "search host with id in cache, but get host failed, err: %v", err)
		return
	}
	ctx.RespString(host)
}

// ListHostWithHostIDInCache list hosts info from redis with host id list.
// if a host is not exist in cache and still can not find in mongodb,
// then it will not be return. so the returned array may not equal to
// the request host ids length and the sequence is also may not same.
func (s *cacheService) ListHostWithHostIDInCache(ctx *rest.Contexts) {
	opt := new(metadata.ListWithIDOption)
	if err := ctx.DecodeInto(&opt); nil != err {
		ctx.RespAutoError(err)
		return
	}

	host, err := s.cacheSet.Host.ListHostWithHostIDs(ctx.Kit.Ctx, opt)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommDBSelectFailed, "list host with id in cache, but get host failed, err: %v", err)
		return
	}
	ctx.RespStringArray(host)
}

func (s *cacheService) ListHostWithPageInCache(ctx *rest.Contexts) {
	opt := new(metadata.ListHostWithPage)
	if err := ctx.DecodeInto(&opt); nil != err {
		ctx.RespAutoError(err)
		return
	}

	cnt, host, err := s.cacheSet.Host.ListHostsWithPage(ctx.Kit.Ctx, opt)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommDBSelectFailed, "list host with id in cache, but get host failed, err: %v", err)
		return
	}
	ctx.RespCountInfoString(cnt, host)
}

// GetHostSnap get one host snap
func (s *cacheService) GetHostSnap(ctx *rest.Contexts) {
	hostID := ctx.Request.PathParameter(common.BKHostIDField)
	key := common.RedisSnapKeyPrefix + hostID
	result, err := redis.Client().Get(ctx.Kit.Ctx, key).Result()
	if nil != err && !redis.IsNilErr(err) {
		blog.Errorf("get host snapshot failed, hostID: %v, err: %v, rid: %s", hostID, err, ctx.Kit.Rid)
		ctx.RespAutoError(ctx.Kit.CCError.CCError(common.CCErrHostGetSnapshot))
		return
	}

	ctx.RespEntity(metadata.HostSnap{
		Data: result,
	})
}

// GetHostSnapBatch get host snap in batch
func (s *cacheService) GetHostSnapBatch(ctx *rest.Contexts) {
	input := metadata.HostSnapBatchInput{}
	if err := ctx.DecodeInto(&input); nil != err {
		ctx.RespAutoError(err)
		return
	}

	if len(input.HostIDs) == 0 {
		ctx.RespEntity(map[int64]string{})
		return
	}

	keys := []string{}
	for _, id := range input.HostIDs {
		keys = append(keys, common.RedisSnapKeyPrefix+strconv.FormatInt(id, 10))
	}

	res, err := redis.Client().MGet(ctx.Kit.Ctx, keys...).Result()
	if err != nil {
		if redis.IsNilErr(err) {
			ctx.RespEntity(map[int64]string{})
			return
		}
		blog.Errorf("get host snapshot failed, keys: %#v, err: %v, rid: %s", keys, err, ctx.Kit.Rid)
		ctx.RespAutoError(ctx.Kit.CCError.CCError(common.CCErrHostGetSnapshot))
		return
	}

	ret := make(map[int64]string)
	for i, hostID := range input.HostIDs {
		if res[i] == nil {
			ret[hostID] = ""
			continue
		}
		value, ok := res[i].(string)
		if !ok {
			blog.Errorf("GetHostSnapBatch failed, hostID: %d, value in redis is not type string, but tyep: %T, value:%#v, rid: %s", hostID, res[i], res[i], ctx.Kit.Rid)
			ret[hostID] = ""
			continue
		}
		ret[hostID] = value
	}

	ctx.RespEntity(ret)
}

// ListBusiness list business with id from cache, if not exist in cache, then get from mongodb directly.
func (s *cacheService) ListBusinessInCache(ctx *rest.Contexts) {
	opt := new(metadata.ListWithIDOption)
	if err := ctx.DecodeInto(&opt); nil != err {
		ctx.RespAutoError(err)
		return
	}

	details, err := s.cacheSet.Business.ListBusiness(ctx.Kit.Ctx, opt)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommDBSelectFailed, "list business with id in cache failed, err: %v", err)
		return
	}
	ctx.RespStringArray(details)
}

// ListModules list modules with id from cache, if not exist in cache, then get from mongodb directly.
func (s *cacheService) ListModulesInCache(ctx *rest.Contexts) {
	opt := new(metadata.ListWithIDOption)
	if err := ctx.DecodeInto(&opt); nil != err {
		ctx.RespAutoError(err)
		return
	}

	details, err := s.cacheSet.Business.ListModules(ctx.Kit.Ctx, opt)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommDBSelectFailed, "list modules with id in cache failed, err: %v", err)
		return
	}
	ctx.RespStringArray(details)
}

// ListSets list sets with id from cache, if not exist in cache, then get from mongodb directly.
func (s *cacheService) ListSetsInCache(ctx *rest.Contexts) {
	opt := new(metadata.ListWithIDOption)
	if err := ctx.DecodeInto(&opt); nil != err {
		ctx.RespAutoError(err)
		return
	}

	details, err := s.cacheSet.Business.ListSets(ctx.Kit.Ctx, opt)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommDBSelectFailed, "list sets with id in cache failed, err: %v", err)
		return
	}
	ctx.RespStringArray(details)
}

func (s *cacheService) SearchBusinessInCache(ctx *rest.Contexts) {
	bizID, err := strconv.ParseInt(ctx.Request.PathParameter(common.BKAppIDField), 10, 64)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommParamsIsInvalid, "invalid biz id")
		return
	}
	biz, err := s.cacheSet.Business.GetBusiness(bizID)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommDBSelectFailed, "search biz with id in cache, but get biz failed, err: %v", err)
		return
	}
	ctx.RespString(biz)
}

func (s *cacheService) SearchSetInCache(ctx *rest.Contexts) {
	setID, err := strconv.ParseInt(ctx.Request.PathParameter(common.BKSetIDField), 10, 64)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommParamsIsInvalid, "invalid set id")
		return
	}

	set, err := s.cacheSet.Business.GetSet(setID)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommDBSelectFailed, "search set with id in cache failed, err: %v", err)
		return
	}
	ctx.RespString(set)
}

func (s *cacheService) SearchModuleInCache(ctx *rest.Contexts) {
	moduleID, err := strconv.ParseInt(ctx.Request.PathParameter(common.BKModuleIDField), 10, 64)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommParamsIsInvalid, "invalid module id")
		return
	}

	module, err := s.cacheSet.Business.GetModuleDetail(moduleID)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommDBSelectFailed, "search module with id in cache failed, err: %v", err)
		return
	}
	ctx.RespString(module)
}

func (s *cacheService) SearchCustomLayerInCache(ctx *rest.Contexts) {
	objID := ctx.Request.PathParameter(common.BKObjIDField)

	instID, err := strconv.ParseInt(ctx.Request.PathParameter(common.BKInstIDField), 10, 64)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommParamsIsInvalid, "invalid instance id")
		return
	}

	inst, err := s.cacheSet.Business.GetCustomLevelDetail(objID, instID)
	if err != nil {
		ctx.RespErrorCodeOnly(common.CCErrCommDBSelectFailed, "search custom layer with id in cache failed, err: %v", err)
		return
	}
	ctx.RespString(inst)
}

// SearchTopologyNodePath is to search biz instance topology node's parent path. eg:
// from itself up to the biz instance, but not contains the node itself.
func (s *cacheService) SearchTopologyNodePath(ctx *rest.Contexts) {
	opt := new(topo_tree.SearchNodePathOption)
	if err := ctx.DecodeInto(&opt); nil != err {
		ctx.RespAutoError(err)
		return
	}

	paths, err := s.cacheSet.Topology.SearchNodePath(ctx.Kit.Ctx, opt)
	if err != nil {
		ctx.RespAutoError(err)
		return
	}

	ctx.RespEntity(paths)
}
