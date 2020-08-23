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

package business

import (
	"context"
	"fmt"
	"strings"

	"configcenter/src/common"
	"configcenter/src/common/blog"
	"configcenter/src/common/json"
	"configcenter/src/common/mapstr"
	"configcenter/src/common/metadata"
	"configcenter/src/source_controller/coreservice/cache/tools"
	"configcenter/src/storage/driver/mongodb"
	"configcenter/src/storage/reflector"

	"gopkg.in/redis.v5"
)

type Client struct {
	rds   *redis.Client
	event reflector.Interface
	lock  tools.RefreshingLock
}

func (c *Client) GetBizBaseList() ([]BizBaseInfo, error) {
	c.tryRefreshBaseList(0, refreshList{
		mainKey:        bizKey.listKeyWithBiz(0),
		lockKey:        bizKey.listLockKeyWithBiz(0),
		expireKey:      bizKey.listExpireKeyWithBiz(0),
		expireDuration: bizKey.listExpireDuration,
		getList:        c.genBusinessListKeys,
	})

	// get the business base info
	base, err := c.getBusinessBaseInfo()
	if err == nil {
		return base, nil
	}

	blog.Errorf("get biz base list from cache failed, will get from mongodb, err: %v", err)
	// get from db directly.
	list := make([]BizBaseInfo, 0)
	err = mongodb.Client().Table(common.BKTableNameBaseApp).Find(nil).Fields(common.BKAppIDField, common.BKAppNameField).All(context.Background(), &list)
	if err != nil {
		blog.Errorf("sync biz list to refresh cache, but get biz list from mongodb failed, err: %v", err)
		return nil, err
	}
	return list, err

}

// get a business's all info.
func (c *Client) GetBusiness(bizID int64) (string, error) {
	key := bizKey.detailKey(bizID)
	exist, err := c.rds.Exists(key).Result()
	if err != nil {
		blog.Warnf("get business info from cache,  biz: %d, but check exist failed, err: %v", bizID, err)
		// get from db directly.
		exist = false
	}

	// try to refresh cache.
	c.tryRefreshInstanceDetail(bizID, refreshInstance{
		mainKey:        bizKey.detailKey(bizID),
		lockKey:        bizKey.detailLockKey(bizID),
		expireKey:      bizKey.detailExpireKey(bizID),
		expireDuration: bizKey.detailExpireDuration,
		getDetail:      c.getBusinessFromMongo,
	})

	if exist {
		biz, err := c.rds.Get(key).Result()
		if err == nil {
			return biz, nil
		}
		// error occurs, get from db directly.
		// Note: this may cause high db query
		blog.Errorf("get business %d info from cache, but failed, will get from mongodb, err: %v", bizID, err)
	}

	// get from db
	return c.getBusinessFromMongo(bizID)
}

func (c *Client) ListBusiness(ctx context.Context, opt *metadata.ListWithIDOption) ([]string, error) {
	rid := ctx.Value(common.ContextRequestIDField)

	keys := make([]string, len(opt.IDs))
	for idx, bizID := range opt.IDs {
		keys[idx] = bizKey.detailKey(bizID)

		// try to refresh cache.
		c.tryRefreshInstanceDetail(bizID, refreshInstance{
			mainKey:        bizKey.detailKey(bizID),
			lockKey:        bizKey.detailLockKey(bizID),
			expireKey:      bizKey.detailExpireKey(bizID),
			expireDuration: bizKey.detailExpireDuration,
			getDetail:      c.getBusinessFromMongo,
		})
	}

	bizList, err := c.rds.MGet(keys...).Result()
	if err != nil {
		blog.Errorf("get business %d info from cache failed, get from db directly, err: %v, rid: %v", opt.IDs, err, rid)
		return c.listBusinessFromMongo(ctx, opt.IDs, opt.Fields)
	}

	all := make([]string, 0)
	toAdd := make([]int64, 0)
	for idx, biz := range bizList {
		if biz == nil {
			// can not find in cache
			toAdd = append(toAdd, opt.IDs[idx])
			continue
		}

		detail, ok := biz.(string)
		if !ok {
			blog.Errorf("got invalid biz cache %v, rid: %v", biz, rid)
			return nil, fmt.Errorf("got invalid biz cache %v", biz)
		}

		if len(opt.Fields) != 0 {
			all = append(all, *json.CutJsonDataWithFields(&detail, opt.Fields))
		} else {
			all = append(all, detail)
		}

	}

	if len(toAdd) != 0 {
		details, err := c.listBusinessFromMongo(ctx, toAdd, opt.Fields)
		if err != nil {
			blog.Errorf("get business list from db failed, err: %v, rid: %v", err, rid)
			return nil, err
		}

		all = append(all, details...)
	}

	return all, nil
}

func (c *Client) ListModules(ctx context.Context, opt *metadata.ListWithIDOption) ([]string, error) {
	rid := ctx.Value(common.ContextRequestIDField)

	keys := make([]string, len(opt.IDs))
	for idx, id := range opt.IDs {
		keys[idx] = moduleKey.detailKey(id)

		// try to refresh cache.
		c.tryRefreshInstanceDetail(id, refreshInstance{
			mainKey:        moduleKey.detailKey(id),
			lockKey:        moduleKey.detailLockKey(id),
			expireKey:      moduleKey.detailExpireKey(id),
			expireDuration: moduleKey.detailExpireDuration,
			getDetail:      c.getModuleDetailFromMongo,
		})
	}

	list, err := c.rds.MGet(keys...).Result()
	if err != nil {
		blog.Errorf("list module %d info from cache failed, get from db directly, err: %v, rid: %v", opt.IDs, err, rid)
		return c.listModuleFromMongo(ctx, opt.IDs, opt.Fields)
	}

	all := make([]string, 0)
	toAdd := make([]int64, 0)
	for idx, module := range list {
		if module == nil {
			// can not find in cache
			toAdd = append(toAdd, opt.IDs[idx])
			continue
		}

		detail, ok := module.(string)
		if !ok {
			blog.Errorf("got invalid module cache %v, rid: %v", module, rid)
			return nil, fmt.Errorf("got invalid module cache %v", module)
		}

		if len(opt.Fields) != 0 {
			all = append(all, *json.CutJsonDataWithFields(&detail, opt.Fields))
		} else {
			all = append(all, detail)
		}
	}

	if len(toAdd) != 0 {
		details, err := c.listModuleFromMongo(ctx, toAdd, opt.Fields)
		if err != nil {
			blog.Errorf("get module list from db failed, err: %v, rid: %v", err, rid)
			return nil, err
		}

		all = append(all, details...)
	}

	return all, nil
}

func (c *Client) ListSets(ctx context.Context, opt *metadata.ListWithIDOption) ([]string, error) {
	rid := ctx.Value(common.ContextRequestIDField)

	keys := make([]string, len(opt.IDs))
	for idx, id := range opt.IDs {
		keys[idx] = setKey.detailKey(id)

		// try to refresh cache.
		c.tryRefreshInstanceDetail(id, refreshInstance{
			mainKey:        setKey.detailKey(id),
			lockKey:        setKey.detailLockKey(id),
			expireKey:      setKey.detailExpireKey(id),
			expireDuration: setKey.detailExpireDuration,
			getDetail:      c.getSetDetailFromMongo,
		})
	}

	list, err := c.rds.MGet(keys...).Result()
	if err != nil {
		blog.Errorf("list set %d info from cache failed, get from db directly, err: %v, rid: %v", opt.IDs, err, rid)
		return c.listSetFromMongo(ctx, opt.IDs, opt.Fields)
	}

	all := make([]string, 0)
	toAdd := make([]int64, 0)
	for idx, set := range list {
		if set == nil {
			// can not find in cache
			toAdd = append(toAdd, opt.IDs[idx])
			continue
		}

		detail, ok := set.(string)
		if !ok {
			blog.Errorf("got invalid set cache %v, rid: %v", set, rid)
			return nil, fmt.Errorf("got invalid set cache %v", set)
		}

		if len(opt.Fields) != 0 {
			all = append(all, *json.CutJsonDataWithFields(&detail, opt.Fields))
		} else {
			all = append(all, detail)
		}
	}

	if len(toAdd) != 0 {
		details, err := c.listSetFromMongo(ctx, toAdd, opt.Fields)
		if err != nil {
			blog.Errorf("get set list from db failed, err: %v, rid: %v", err, rid)
			return nil, err
		}

		all = append(all, details...)
	}

	return all, nil
}

func (c *Client) GetModuleBaseList(bizID int64) ([]ModuleBaseInfo, error) {
	c.tryRefreshBaseList(bizID, refreshList{
		mainKey:        moduleKey.listKeyWithBiz(bizID),
		lockKey:        moduleKey.listLockKeyWithBiz(bizID),
		expireKey:      moduleKey.listExpireKeyWithBiz(bizID),
		expireDuration: moduleKey.listExpireDuration,
		getList:        c.genModuleListKeys,
	})
	base, err := c.getModuleBaseList(bizID)
	if err == nil {
		return base, nil
	}

	blog.Errorf("get biz %d module base list from cache failed, get from db now, err: %v", bizID, err)
	// do not return
	// get from db directly.
	list := make([]ModuleBaseInfo, 0)
	filter := mapstr.MapStr{
		common.BKAppIDField: bizID,
	}

	err = mongodb.Client().Table(common.BKTableNameBaseModule).Find(filter).All(context.Background(), &list)
	if err != nil {
		return nil, err
	}

	return list, nil
}

func (c *Client) GetModule(moduleID int64) (string, error) {
	// try refresh the module cache
	c.tryRefreshInstanceDetail(moduleID, refreshInstance{
		mainKey:        moduleKey.detailKey(moduleID),
		lockKey:        moduleKey.detailLockKey(moduleID),
		expireKey:      moduleKey.detailExpireKey(moduleID),
		expireDuration: moduleKey.detailExpireDuration,
		getDetail:      c.getModuleDetailFromMongo,
	})

	mod, err := c.rds.Get(moduleKey.detailKey(moduleID)).Result()
	if err == nil && len(mod) != 0 {
		return mod, nil
	}
	blog.Errorf("get module: %d failed from redis, err: %v", err)
	// get from db directly.
	return c.getModuleDetailFromMongo(moduleID)
}

func (c *Client) GetSetBaseList(bizID int64) ([]SetBaseInfo, error) {
	c.tryRefreshBaseList(bizID, refreshList{
		mainKey:        setKey.listKeyWithBiz(bizID),
		lockKey:        setKey.listLockKeyWithBiz(bizID),
		expireKey:      setKey.listExpireKeyWithBiz(bizID),
		expireDuration: setKey.listExpireDuration,
		getList:        c.genSetListKeys,
	})
	base, err := c.getSetBaseList(bizID)
	if err == nil {
		return base, nil
	}

	blog.Errorf("get biz %d set base list from cache failed, get from db now, err: %v", bizID, err)
	// do not return
	// get from db directly.
	list := make([]SetBaseInfo, 0)
	filter := mapstr.MapStr{
		common.BKAppIDField: bizID,
	}

	err = mongodb.Client().Table(common.BKTableNameBaseSet).Find(filter).All(context.Background(), &list)
	if err != nil {
		return nil, err
	}

	return list, nil
}

func (c *Client) GetSet(setID int64) (string, error) {
	// try refresh the module cache
	c.tryRefreshInstanceDetail(setID, refreshInstance{
		mainKey:        setKey.detailKey(setID),
		lockKey:        setKey.detailLockKey(setID),
		expireKey:      setKey.detailExpireKey(setID),
		expireDuration: setKey.detailExpireDuration,
		getDetail:      c.getSetDetailFromMongo,
	})

	set, err := c.rds.Get(setKey.detailKey(setID)).Result()
	if err == nil && len(set) != 0 {
		return set, nil
	}
	blog.Errorf("get set: %d failed from redis, err: %v", err)
	// get from db directly.
	return c.getSetDetailFromMongo(setID)
}

func (c *Client) GetCustomLevelBaseList(objectID string, bizID int64) ([]CustomInstanceBase, error) {
	c.tryRefreshBaseList(bizID, refreshList{
		mainKey:        customKey.objListKeyWithBiz(objectID, bizID),
		lockKey:        customKey.objListLockKeyWithBiz(objectID, bizID),
		expireKey:      customKey.objListExpireKeyWithBiz(objectID, bizID),
		expireDuration: customKey.listExpireDuration,
		getList: func(bizID int64) (strings []string, err error) {
			return c.genCustomLevelListKeys(objectID, bizID)
		},
	})

	list, err := c.getCustomLevelBaseList(objectID, bizID)
	if err == nil {
		return list, nil
	}
	blog.Errorf("get biz: %s, obj: %s custom level list keys from cache failed, will get from mongodb, err: %v",
		bizID, objectID, err)

	return c.getCustomLevelBaseFromMongodb(objectID, bizID)
}

func (c *Client) GetCustomLevelDetail(objID string, instID int64) (string, error) {
	c.tryRefreshInstanceDetail(instID, refreshInstance{
		mainKey:        customKey.detailKey(objID, instID),
		lockKey:        customKey.detailLockKey(objID, instID),
		expireKey:      customKey.detailExpireKey(objID, instID),
		expireDuration: customKey.detailExpireDuration,
		getDetail: func(instID int64) (s string, err error) {
			return c.getCustomLevelDetail(objID, instID)
		},
	})

	set, err := c.rds.Get(customKey.detailKey(objID, instID)).Result()
	if err == nil && len(set) != 0 {
		return set, nil
	}
	blog.Errorf("get biz custom level, obj:%s, inst: %d failed from redis, err: %v", objID, instID, err)
	// get from db directly.
	return c.getCustomLevelDetail(objID, instID)
}

func (c *Client) GetTopology() ([]string, error) {
	// TODO: try refresh the cache.
	key := customKey.topologyKey()
	rank, err := c.rds.Get(key).Result()
	if err != nil {
		return nil, err
	}
	return strings.Split(rank, ","), nil
}
