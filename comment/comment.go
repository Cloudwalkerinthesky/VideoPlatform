package comment

import (
	"Project01/db"
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type PostCommentRequest struct {
	//应该不需要手动写用户名或者用户id,应该从token中解析
	//那视频id呢？能不能从上下文中获取？还是要自己定义？
	UserId          uint64 //用户不需要填
	Username        string //用户不需要填
	VideoId         uint64 `json:"video_id"` //?
	Content         string `json:"content"`
	ParentCommentId uint64 //不是必要的，可以填可以不填
	LikeCount       uint64 //不是必要的，可以填可以不填
	//要包含token吗
}
type PostCommentReply struct {
	Message string
	Data    interface{}
}

// 发布评论
func PostCommentHandler(c *gin.Context) {
	//1.解析客户端传过来的评论Json形式
	var commentReq PostCommentRequest
	userIdA, exists1 := c.Get("user_id") //"user_id"和"username"是从Claims来吗
	usernameA, exists2 := c.Get("username")
	if !exists1 || !exists2 {
		c.JSON(500, gin.H{"error": "(评论模块)从jwt token中解析用户ID和用户名的过程失败，检查jwt中间件的逻辑"})
		return
	}
	//类型断言和类型转换
	userIdFloat64, ok := userIdA.(float64)
	if !ok {
		c.JSON(500, gin.H{"error": "(评论模块)userId类型转换失败"})
		return
	}
	userId := uint64(userIdFloat64)

	str, ok := usernameA.(string)
	if !ok {
		c.JSON(500, gin.H{"error": "(评论模块)username类型转换失败,username不是string"})
		return
	}
	username := str
	//-------
	commentReq.UserId = userId
	commentReq.Username = username
	err := c.ShouldBindJSON(&commentReq) //userId和username会被覆盖吗
	if err != nil {
		c.JSON(400, gin.H{"error": "参数错误"})
		return
	}

	//2.把评论写入数据库
	var comment db.Comment
	database := db.GetDB()
	comment = db.Comment{
		VideoId:     commentReq.VideoId,
		CommenterId: commentReq.UserId,
		Content:     commentReq.Content,
		//CommentTime 会自动创建吧，我这里不用写了吗？是的。
		ParentCommentId: commentReq.ParentCommentId,
		LikeCount:       commentReq.LikeCount,
	}
	result := database.Create(&comment)
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "向数据库中写入评论失败"})
		return
	}

	//3.给客户端响应信息
	c.JSON(200, PostCommentReply{
		Message: "成功发布一条评论",
		Data: gin.H{
			"user_id":  userId,
			"username": username,
		},
	})
}

// 删除评论
func DeleteCommentHandler(c *gin.Context) {
	//1.获取客户端通过URL路径参数传过来的commentID
	commentIdStr := c.Param("id")
	commentId, err := strconv.ParseUint(commentIdStr, 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "评论ID的string->uint64转换失败,客户端传的ID不合法"})
		return
	}
	//2.校验当前客户端身份：只有评论的发布者和管理员才能删除
	//获取当前用户id
	rawUserId, exists := c.Get("user_id") //获取的是any类型的
	if !exists {
		c.JSON(500, gin.H{"error": "用户ID获取失败"})
		return
	}
	userIdFloat64, ok := rawUserId.(float64)
	if !ok {
		c.JSON(500, gin.H{"error": "用户ID类型转换失败"})
		return
	}
	userId := uint64(userIdFloat64)

	database := db.GetDB() //获取数据库句柄
	var comment db.Comment //定义结构体变量，稍后查询结果会被填入这里
	//先检查评论是否存在
	err = database.Where("id=?", commentId).First(&comment).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "评论不存在"})
		} else {
			c.JSON(500, gin.H{"error": "查询评论失败"})
		}
		return
	}
	//再检查权限
	//如果当前用户不是该评论发布者，没有权限删除该评论
	if comment.CommenterId != userId {
		c.JSON(403, gin.H{"error": "您无权限删除该评论"})
		return
	}

	//3.删除评论
	// result := database.Where("id=?", commentId).Delete(&db.Comment{}) //删除comments表上的commentId对应的一行
	result := database.Delete(&comment)
	if result.Error != nil {
		c.JSON(500, gin.H{"error": "删除评论失败"})
		return
	}
	c.JSON(200, gin.H{"message": "删除评论成功"})
}
