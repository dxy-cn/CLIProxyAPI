package openai

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
)

func readOpenAIRawJSON(c *gin.Context) ([]byte, bool) {
	rawJSON, err := handlers.ReadLimitedRawData(c)
	if err != nil {
		c.JSON(handlers.RequestBodyErrorStatus(err), handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return nil, false
	}
	return rawJSON, true
}
