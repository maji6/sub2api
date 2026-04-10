package audithttp

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api-audit/internal/repository"
	"github.com/gin-gonic/gin"
)

func AdminAPIKeyAuth(settingsRepo *repository.SettingsRepository, overrideKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		provided := strings.TrimSpace(c.GetHeader("x-api-key"))
		if provided == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"code":    "UNAUTHORIZED",
					"message": "x-api-key is required",
				},
			})
			return
		}

		expected := strings.TrimSpace(overrideKey)
		if expected == "" {
			var err error
			expected, err = settingsRepo.GetAdminAPIKey(c.Request.Context())
			if err != nil && !errors.Is(err, repository.ErrSettingNotFound) {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error": gin.H{
						"code":    "INTERNAL_ERROR",
						"message": "failed to load admin api key",
					},
				})
				return
			}
		}

		if expected == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"code":    "INVALID_ADMIN_KEY",
					"message": "invalid admin api key",
				},
			})
			return
		}

		c.Next()
	}
}
