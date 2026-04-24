package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func ExternalApiAuth() func(*gin.Context) {
	return func(c *gin.Context) {
		common.OptionMapRWMutex.RLock()
		configuredKey := common.OptionMap["ExternalApiKey"]
		common.OptionMapRWMutex.RUnlock()

		if configuredKey == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"message": "外部接口未启用，请管理员在系统设置中配置 ExternalApiKey",
			})
			c.Abort()
			return
		}

		requestKey := c.GetHeader("X-External-Api-Key")
		if requestKey == "" || requestKey != configuredKey {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "X-External-Api-Key 无效或缺失",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
