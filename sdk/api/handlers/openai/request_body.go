package openai

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
)

const maxOpenAIResponsesRequestBodyBytes int64 = 64 * 1024 * 1024

func readOpenAIRawJSON(c *gin.Context) ([]byte, bool) {
	return readOpenAIRawJSONWithLimit(c, handlers.MaxRequestBodyBytes)
}

func readOpenAIResponsesRawJSON(c *gin.Context) ([]byte, bool) {
	return readOpenAIRawJSONWithLimit(c, maxOpenAIResponsesRequestBodyBytes)
}

func readOpenAIRawJSONWithLimit(c *gin.Context, maxBytes int64) ([]byte, bool) {
	rawJSON, err := handlers.ReadLimitedRawDataWithLimit(c, maxBytes)
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
