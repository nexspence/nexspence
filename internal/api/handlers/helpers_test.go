package handlers_test

import (
	"context"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func testContext() context.Context { return context.Background() }
