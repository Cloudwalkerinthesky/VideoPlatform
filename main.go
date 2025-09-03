package main

import (
	"Project01/comment"
	"Project01/db"
	"Project01/login"
	"Project01/video"

	"github.com/gin-gonic/gin"
)

func main() {
	//初始化数据库连接(全局变量db会在此处被赋值)
	db.InitDB()

	//初始化MinIO
	video.InitMinio()

	//启动Gin引擎
	r := gin.Default()

	//测试ping
	r.GET("/ping", func(ctx *gin.Context) {
		ctx.JSON(200, gin.H{"message": "pong"})
	})

	//登录
	r.POST("/login", login.LoginHandler)

	//鉴权
	auth := r.Group("/jwt", login.AuthMiddleware())
	{ //需要鉴权的操作
		//上传视频
		auth.POST("/upload", video.UploadVideoHandler)
		//播放视频 动态定义参数filename
		auth.GET("/video/:filename", video.PlayVideoHandler)
		//发布评论
		auth.POST("/comment", comment.PostCommentHandler)
		//删除评论
		auth.DELETE("/comment/:id", comment.DeleteCommentHandler)
	}

	//启动HTTP服务
	r.Run("0.0.0.0:8080")
}
