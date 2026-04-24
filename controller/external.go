package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ===== 追加请求 DTO =====

type ExternalUpdateUserRequest struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Group       string `json:"group"`
	Password    string `json:"password"`
}

type ExternalUpdateTokenRequest struct {
	Name           string `json:"name"`
	ExpiredTime    *int64 `json:"expired_time"`
	UnlimitedQuota *bool  `json:"unlimited_quota"`
	RemainQuota    *int   `json:"remain_quota"`
}

type ExternalTokenStatusRequest struct {
	Action string `json:"action" binding:"required,oneof=enable disable"`
}

type ExternalBatchQuotaItem struct {
	UserId int    `json:"user_id" binding:"required"`
	Mode   string `json:"mode" binding:"required,oneof=add subtract set"`
	Value  int    `json:"value" binding:"min=0"`
}

type ExternalBatchQuotaRequest struct {
	Items []ExternalBatchQuotaItem `json:"items" binding:"required,min=1,max=100"`
}

// ===== 请求 DTO =====

type ExternalCreateUserRequest struct {
	Username    string `json:"username" binding:"required,min=3,max=20"`
	Password    string `json:"password" binding:"required,min=8,max=20"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Group       string `json:"group"`
	Quota       int    `json:"quota"`
}

type ExternalQuotaRequest struct {
	Mode  string `json:"mode" binding:"required,oneof=add subtract set"`
	Value int    `json:"value" binding:"min=0"`
}

type ExternalStatusRequest struct {
	Action string `json:"action" binding:"required,oneof=enable disable"`
}

type ExternalCreateTokenRequest struct {
	Name           string `json:"name"`
	ExpiredTime    int64  `json:"expired_time"`
	UnlimitedQuota bool   `json:"unlimited_quota"`
	RemainQuota    int    `json:"remain_quota"`
}

// ===== 辅助函数 =====

func externalUserResponse(user *model.User) gin.H {
	return gin.H{
		"id":            user.Id,
		"username":      user.Username,
		"display_name":  user.DisplayName,
		"email":         user.Email,
		"quota":         user.Quota,
		"used_quota":    user.UsedQuota,
		"status":        user.Status,
		"group":         user.Group,
		"role":          user.Role,
		"request_count": user.RequestCount,
	}
}

// ===== 用户管理 =====

// ExternalCreateUser POST /api/external/user
// 幂等：用户名已存在返回现有用户，不返回错误
func ExternalCreateUser(c *gin.Context) {
	var req ExternalCreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	var existing model.User
	err := model.DB.Where("username = ?", req.Username).First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if existing.Id != 0 {
		data := externalUserResponse(&existing)
		data["created"] = false
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "用户已存在", "data": data})
		return
	}

	group := req.Group
	if group == "" {
		group = "default"
	}
	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Username
	}

	user := model.User{
		Username:    req.Username,
		Password:    req.Password,
		DisplayName: displayName,
		Email:       req.Email,
		Group:       group,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if err := user.Insert(0); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	if req.Quota > 0 {
		if err := model.IncreaseUserQuota(user.Id, req.Quota, true); err != nil {
			common.SysLog(fmt.Sprintf("external: failed to set initial quota for user %d: %v", user.Id, err))
		} else {
			model.RecordLog(user.Id, model.LogTypeManage,
				fmt.Sprintf("外部系统创建用户，初始额度 %s", logger.LogQuota(req.Quota)))
		}
	}

	data := externalUserResponse(&user)
	data["created"] = true
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": data})
}

// ExternalListUser GET /api/external/user?username=xxx
func ExternalListUser(c *gin.Context) {
	username := c.Query("username")
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "username 参数必填"})
		return
	}

	var user model.User
	if err := model.DB.Where("username = ?", username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "用户不存在"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": externalUserResponse(&user)})
}

// ExternalGetUser GET /api/external/user/:id
func ExternalGetUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的用户 ID"})
		return
	}

	user, err := model.GetUserById(id, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "用户不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": externalUserResponse(user)})
}

// ExternalUpdateQuota PUT /api/external/user/:id/quota
func ExternalUpdateQuota(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的用户 ID"})
		return
	}

	var req ExternalQuotaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	user, err := model.GetUserById(id, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "用户不存在"})
		return
	}

	switch req.Mode {
	case "add":
		if req.Value <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "增加额度必须大于 0"})
			return
		}
		if err := model.IncreaseUserQuota(user.Id, req.Value, true); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		model.RecordLog(user.Id, model.LogTypeManage,
			fmt.Sprintf("外部系统增加用户额度 %s", logger.LogQuota(req.Value)))
	case "subtract":
		if req.Value <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "减少额度必须大于 0"})
			return
		}
		if err := model.DecreaseUserQuota(user.Id, req.Value, true); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		model.RecordLog(user.Id, model.LogTypeManage,
			fmt.Sprintf("外部系统减少用户额度 %s", logger.LogQuota(req.Value)))
	case "set":
		if err := model.DB.Model(&model.User{}).Where("id = ?", user.Id).
			Update("quota", req.Value).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		if err := model.InvalidateUserCache(user.Id); err != nil {
			common.SysLog(fmt.Sprintf("external: failed to invalidate cache for user %d: %v", user.Id, err))
		}
		model.RecordLog(user.Id, model.LogTypeManage,
			fmt.Sprintf("外部系统覆盖用户额度从 %s 为 %s", logger.LogQuota(user.Quota), logger.LogQuota(req.Value)))
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"user_id": user.Id,
			"mode":    req.Mode,
			"delta":   req.Value,
		},
	})
}

// ExternalUpdateStatus PUT /api/external/user/:id/status
func ExternalUpdateStatus(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的用户 ID"})
		return
	}

	var req ExternalStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	user, err := model.GetUserById(id, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "用户不存在"})
		return
	}

	switch req.Action {
	case "enable":
		user.Status = common.UserStatusEnabled
	case "disable":
		user.Status = common.UserStatusDisabled
	}

	if err := user.Update(false); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := model.InvalidateUserCache(user.Id); err != nil {
		common.SysLog(fmt.Sprintf("external: failed to invalidate cache for user %d: %v", user.Id, err))
	}
	if err := model.InvalidateUserTokensCache(user.Id); err != nil {
		common.SysLog(fmt.Sprintf("external: failed to invalidate tokens cache for user %d: %v", user.Id, err))
	}
	model.RecordLog(user.Id, model.LogTypeManage,
		fmt.Sprintf("外部系统设置用户状态: %s", req.Action))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"user_id": user.Id, "status": user.Status},
	})
}

// ExternalDeleteUser DELETE /api/external/user/:id
func ExternalDeleteUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的用户 ID"})
		return
	}

	user, err := model.GetUserById(id, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "用户不存在"})
		return
	}

	if err := user.Delete(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := model.InvalidateUserTokensCache(user.Id); err != nil {
		common.SysLog(fmt.Sprintf("external: failed to invalidate tokens cache for user %d: %v", user.Id, err))
	}
	model.RecordLog(user.Id, model.LogTypeManage, "外部系统删除用户")

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"user_id": user.Id}})
}

// ===== Token（API Key）管理 =====

// ExternalCreateToken POST /api/external/user/:id/token
// 返回完整 sk-key，这是唯一能获取完整 key 的时机
func ExternalCreateToken(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil || userId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的用户 ID"})
		return
	}

	if _, err := model.GetUserById(userId, false); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "用户不存在"})
		return
	}

	var req ExternalCreateTokenRequest
	// ShouldBindJSON 仅在有 body 时解析，忽略空 body 错误
	_ = c.ShouldBindJSON(&req)

	name := req.Name
	if name == "" {
		name = "外部系统创建"
	}
	expiredTime := req.ExpiredTime
	if expiredTime == 0 {
		expiredTime = -1 // 永不过期
	}

	key, err := common.GenerateKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "生成 key 失败"})
		common.SysLog("external: failed to generate token key: " + err.Error())
		return
	}

	token := model.Token{
		UserId:         userId,
		Name:           name,
		Key:            key,
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    expiredTime,
		UnlimitedQuota: req.UnlimitedQuota,
		RemainQuota:    req.RemainQuota,
		Status:         common.TokenStatusEnabled,
	}
	if err := token.Insert(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"id":              token.Id,
			"key":             key, // 完整 key，仅此一次
			"name":            token.Name,
			"expired_time":    token.ExpiredTime,
			"unlimited_quota": token.UnlimitedQuota,
			"remain_quota":    token.RemainQuota,
		},
	})
}

// ExternalListTokens GET /api/external/user/:id/tokens
func ExternalListTokens(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil || userId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的用户 ID"})
		return
	}

	if _, err := model.GetUserById(userId, false); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "用户不存在"})
		return
	}

	tokens, err := model.GetAllUserTokens(userId, 0, 200)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	result := make([]gin.H, 0, len(tokens))
	for _, t := range tokens {
		result = append(result, gin.H{
			"id":              t.Id,
			"name":            t.Name,
			"key":             model.MaskTokenKey(t.Key), // 脱敏
			"status":          t.Status,
			"remain_quota":    t.RemainQuota,
			"used_quota":      t.UsedQuota,
			"unlimited_quota": t.UnlimitedQuota,
			"expired_time":    t.ExpiredTime,
			"created_time":    t.CreatedTime,
			"accessed_time":   t.AccessedTime,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": result})
}

// ExternalDeleteUserTokens DELETE /api/external/user/:id/tokens
func ExternalDeleteUserTokens(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil || userId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的用户 ID"})
		return
	}

	if _, err := model.GetUserById(userId, false); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "用户不存在"})
		return
	}

	// 先失效 Redis 缓存（遍历所有 token key），再软删除
	if err := model.InvalidateUserTokensCache(userId); err != nil {
		common.SysLog(fmt.Sprintf("external: failed to invalidate tokens cache for user %d: %v", userId, err))
	}

	result := model.DB.Where("user_id = ?", userId).Delete(&model.Token{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": result.Error.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("已删除 %d 个 Token", result.RowsAffected),
		"data":    gin.H{"user_id": userId, "deleted_count": result.RowsAffected},
	})
}

// ExternalDeleteToken DELETE /api/external/token/:token_id
func ExternalDeleteToken(c *gin.Context) {
	tokenId, err := strconv.Atoi(c.Param("token_id"))
	if err != nil || tokenId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的 Token ID"})
		return
	}

	token, err := model.GetTokenById(tokenId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Token 不存在"})
		return
	}

	if err := token.Delete(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"token_id": tokenId, "user_id": token.UserId},
	})
}

// ===== 扩展接口 =====

// ExternalUpdateUser PUT /api/external/user/:id
// 更新用户信息（display_name / email / group / password，均可选）
func ExternalUpdateUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的用户 ID"})
		return
	}
	var req ExternalUpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	user, err := model.GetUserById(id, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "用户不存在"})
		return
	}

	updatePassword := false
	if req.DisplayName != "" {
		user.DisplayName = req.DisplayName
	}
	if req.Email != "" {
		user.Email = req.Email
	}
	if req.Group != "" {
		user.Group = req.Group
	}
	if req.Password != "" {
		user.Password = req.Password
		updatePassword = true
	}

	if err := user.Update(updatePassword); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := model.InvalidateUserCache(user.Id); err != nil {
		common.SysLog(fmt.Sprintf("external: failed to invalidate cache for user %d: %v", user.Id, err))
	}
	model.RecordLog(user.Id, model.LogTypeManage, "外部系统更新用户信息")

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": externalUserResponse(user)})
}

// ExternalUpdateToken PUT /api/external/token/:token_id
// 更新 token 属性（name / expired_time / remain_quota / unlimited_quota）
func ExternalUpdateToken(c *gin.Context) {
	tokenId, err := strconv.Atoi(c.Param("token_id"))
	if err != nil || tokenId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的 Token ID"})
		return
	}
	var req ExternalUpdateTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	token, err := model.GetTokenById(tokenId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Token 不存在"})
		return
	}

	if req.Name != "" {
		token.Name = req.Name
	}
	if req.ExpiredTime != nil {
		token.ExpiredTime = *req.ExpiredTime
	}
	if req.UnlimitedQuota != nil {
		token.UnlimitedQuota = *req.UnlimitedQuota
	}
	if req.RemainQuota != nil {
		token.RemainQuota = *req.RemainQuota
	}

	if err := token.Update(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"id":              token.Id,
			"name":            token.Name,
			"expired_time":    token.ExpiredTime,
			"unlimited_quota": token.UnlimitedQuota,
			"remain_quota":    token.RemainQuota,
			"status":          token.Status,
		},
	})
}

// ExternalUpdateTokenStatus PUT /api/external/token/:token_id/status
func ExternalUpdateTokenStatus(c *gin.Context) {
	tokenId, err := strconv.Atoi(c.Param("token_id"))
	if err != nil || tokenId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的 Token ID"})
		return
	}
	var req ExternalTokenStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	token, err := model.GetTokenById(tokenId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Token 不存在"})
		return
	}

	switch req.Action {
	case "enable":
		token.Status = common.TokenStatusEnabled
	case "disable":
		token.Status = common.TokenStatusDisabled
	}

	if err := token.Update(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"token_id": token.Id, "status": token.Status},
	})
}

// ExternalGetUserLogs GET /api/external/user/:id/logs
// 查询参数：page(1起)、page_size(默认20最大100)、type(0=全部)、
//            start_time、end_time(Unix秒)、model_name
func ExternalGetUserLogs(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil || userId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的用户 ID"})
		return
	}
	if _, err := model.GetUserById(userId, false); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "用户不存在"})
		return
	}

	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page <= 0 {
		page = 1
	}
	logType, _ := strconv.Atoi(c.DefaultQuery("type", "0"))
	startTs, _ := strconv.ParseInt(c.Query("start_time"), 10, 64)
	endTs, _ := strconv.ParseInt(c.Query("end_time"), 10, 64)
	modelName := c.Query("model_name")

	logs, total, err := model.GetUserLogs(userId, logType, startTs, endTs, modelName, "", (page-1)*pageSize, pageSize, "", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"total":     total,
			"page":      page,
			"page_size": pageSize,
			"items":     logs,
		},
	})
}

// ExternalBatchUpdateQuota POST /api/external/users/batch/quota
// 最多 100 条，失败的单条不影响其他
func ExternalBatchUpdateQuota(c *gin.Context) {
	var req ExternalBatchQuotaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	type itemResult struct {
		UserId  int    `json:"user_id"`
		Success bool   `json:"success"`
		Message string `json:"message,omitempty"`
	}
	results := make([]itemResult, 0, len(req.Items))

	for _, item := range req.Items {
		res := itemResult{UserId: item.UserId, Success: true}
		var opErr error

		switch item.Mode {
		case "add":
			opErr = model.IncreaseUserQuota(item.UserId, item.Value, true)
		case "subtract":
			opErr = model.DecreaseUserQuota(item.UserId, item.Value, true)
		case "set":
			opErr = model.DB.Model(&model.User{}).Where("id = ?", item.UserId).
				Update("quota", item.Value).Error
			if opErr == nil {
				_ = model.InvalidateUserCache(item.UserId)
			}
		}

		if opErr != nil {
			res.Success = false
			res.Message = opErr.Error()
		} else {
			model.RecordLog(item.UserId, model.LogTypeManage,
				fmt.Sprintf("外部系统批量%s额度 %s", item.Mode, logger.LogQuota(item.Value)))
		}
		results = append(results, res)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": results})
}

// ExternalGetStats GET /api/external/stats
func ExternalGetStats(c *gin.Context) {
	var totalUsers, activeUsers int64
	model.DB.Model(&model.User{}).Count(&totalUsers)
	model.DB.Model(&model.User{}).Where("status = ?", common.UserStatusEnabled).Count(&activeUsers)

	// 今日 00:00 Unix 时间戳
	now := common.GetTimestamp()
	todayStart := now - (now % 86400)
	var todayNewUsers int64
	model.DB.Model(&model.User{}).Where("created_at >= ?", todayStart).Count(&todayNewUsers)

	var totalUsedQuota, totalQuota struct{ Val int64 }
	model.DB.Model(&model.User{}).Select("COALESCE(SUM(used_quota), 0) as val").Scan(&totalUsedQuota)
	model.DB.Model(&model.User{}).Select("COALESCE(SUM(quota), 0) as val").Scan(&totalQuota)

	var totalTokens int64
	model.DB.Model(&model.Token{}).Count(&totalTokens)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"total_users":      totalUsers,
			"active_users":     activeUsers,
			"today_new_users":  todayNewUsers,
			"total_used_quota": totalUsedQuota.Val,
			"total_quota":      totalQuota.Val,
			"total_tokens":     totalTokens,
		},
	})
}
