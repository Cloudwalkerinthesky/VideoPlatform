package video

import (
	"Project01/db"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"gorm.io/gorm"
)

// 高层封装的MinIO客户端。
// 常用API:PutObject上传对象，GetObject下载对象，ComposeObject合并对象，RemoveObject 删除对象
var minioClient *minio.Client

// 底层的Core客户端。提供接近原始S3协议的接口：NewMultipartUpload,PutObjectPart,CompleteMultipartUpload,AbortMultipartUpload
var minioCore *minio.Core

// 初始化MinIO,返回MinIO客户端对象的指针和错误类型
func InitMinio() (*minio.Client, error) {
	endpoint := "localhost:9000"
	accessKeyID := "minioadmin"
	secretAccessKey := "minioadmin"
	useSSL := false
	//初始化MinIO客户端
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}
	minioClient = client //赋值给全局变量

	//新增：初始化MinIO的Core客户端
	core, err := minio.NewCore(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}
	minioCore = core //赋值给全局变量

	return client, nil
}

// Range格式: Range: bytes=<start>-<end>
// 截取Range
func parseRange(rangeHeader string, totalSize int64) (int64, int64, error) {
	if !strings.HasPrefix(rangeHeader, "bytes=") {
		return 0, 0, errors.New("无效的Range格式")
	}
	trimmed := strings.TrimPrefix(rangeHeader, "bytes=") //<start>-<end>
	splited := strings.Split(trimmed, "-")               //一个切片,第一个元素是start，第二个元素是end
	if len(splited) != 2 {
		return 0, 0, errors.New("Range的格式不完整")
	}
	start, err1 := strconv.ParseInt(splited[0], 10, 64) //10进制，int64
	if err1 != nil {
		return 0, 0, errors.New("Range的start解析错误")
	}

	var end int64
	var err2 error
	if splited[1] == "" {
		end = totalSize - 1
	} else {
		end, err2 = strconv.ParseInt(splited[1], 10, 64)
		if err2 != nil {
			return 0, 0, errors.New("Range的end解析错误")
		}
	}
	return start, end, nil
}

func UploadVideoHandler(c *gin.Context) {
	//获取上传的文件
	file, err := c.FormFile("file") //查找字段名叫“file”的上传内容
	if err != nil {
		c.JSON(400, gin.H{"error": "获取上传文件失败"}) //因为是客户端请求格式不对所以是400 Bad Request
		return
	}
	//打开文件内容
	src, err := file.Open()
	if err != nil {
		c.JSON(500, gin.H{"error": "读取文件失败 " + err.Error()})
		return
	}
	defer src.Close()
	//上传文件到MinIO
	uploadInfo, err := minioClient.PutObject(
		c,             //Context
		"videos",      //桶(Bucket)的名字
		file.Filename, //对象名称，用上传的文件名
		src,           //Reader: 内容来源(流)
		file.Size,     //文件的size
		minio.PutObjectOptions{ContentType: file.Header.Get("Content-Type")},
		//表示从 HTTP 请求的头信息中取出 "Content-Type" 字段(文件的内容类型)的值
		//把取出来的值(如video/mp4)赋给PutObjectOptions这个结构体的ContentType字段
	)
	if err != nil {
		c.JSON(500, gin.H{"error": "上传文件到MinIO过程失败 " + err.Error()})
		return
	}

	//获取上传者ID
	userId, exists := c.Get("user_id") //返回interface{}类型和bool类型
	if !exists {
		//理论上上传者应该存在，如果不存在说明JWT中间件没有正常工作
		c.JSON(500, gin.H{"error": "获取上传者用户Id失败,检查JWT中间件"})
		return
	}
	//类型断言
	userIdFloat64, ok := userId.(float64) //Jwt解析数字时默认为为float64类型
	if !ok {
		c.JSON(500, gin.H{"error": "上传者id类型转换失败"})
		return
	}
	//类型转换
	userIdUint64 := uint64(userIdFloat64)

	//传给数据库的变量
	videoInfo := db.VideoInfo{
		FileName:   file.Filename,
		Size:       file.Size,
		UploaderId: userIdUint64,
	}

	//把视频信息写入数据库
	database := db.GetDB()
	database.Create(&videoInfo)

	//成功响应
	c.JSON(200, gin.H{
		"message":     "上传成功",
		"filename":    file.Filename,
		"bucket":      "videos",
		"object_info": uploadInfo,
	})
}

func PlayVideoHandler(c *gin.Context) {
	//获取视频文件名
	filename := c.Param("filename")
	if filename == "" {
		c.JSON(400, gin.H{"error": "文件名不能为空"})
		return
	}
	//从MinIO中获取文件对象
	obj, err := minioClient.GetObject(c, "videos", filename, minio.GetObjectOptions{})
	if err != nil {
		c.JSON(500, gin.H{"error": "无法获取视频文件 " + err.Error()})
		return
	}
	//获取对象元信息
	metaInfo, err := obj.Stat()
	if err != nil {
		c.JSON(404, gin.H{"error": "视频不存在"})
		return
	}
	totalSize := metaInfo.Size

	//判断文件类型
	var contentType string
	switch {
	case strings.HasSuffix(filename, ".mp4"):
		contentType = "video/mp4"
	case strings.HasSuffix(filename, ".webm"):
		contentType = "video/webm"
	case strings.HasSuffix(filename, ".avi"):
		contentType = "video/x-msvideo"
	case strings.HasSuffix(filename, ".mov"):
		contentType = "video/quicktime"
	case strings.HasSuffix(filename, ".mkv"):
		contentType = "video/x-matroska"
	default:
		contentType = "application/octet-stream" //通用二进制数据
	}
	//获取range
	rangeHeader := c.GetHeader("Range")
	//没有Range,直接返回整个视频
	if rangeHeader == "" {
		c.Header("Content-Type", contentType)
		c.Header("Content-Length", fmt.Sprintf("%d", totalSize))
		c.Header("Accept-Ranges", "bytes")
		c.Header("Content-Disposition", "inline")
		_, err := io.Copy(c.Writer, obj)
		if err != nil {
			c.JSON(500, gin.H{"error": "(在没有Range的前提下返回整个视频)视频传输失败" + err.Error()})
			return
		}
		return
	}
	//有Range
	start, end, err := parseRange(rangeHeader, totalSize)
	if err != nil || start >= totalSize {
		//让浏览器重新发Range
		c.Header("Content-Range", fmt.Sprintf("bytes */%d", totalSize))
		c.Status(416)
		return
	}
	if end >= totalSize {
		end = totalSize - 1
	}
	length := end - start + 1
	//定位指针
	_, err = obj.Seek(start, io.SeekStart)
	if err != nil {
		c.JSON(500, gin.H{"error": "Seek失败" + err.Error()})
		return
	}
	//设置响应头
	c.Status(206) //206 服务端返回部分资源
	c.Header("Content-Type", contentType)
	c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, totalSize))
	c.Header("Content-Length", fmt.Sprintf("%d", length))
	c.Header("Accept-Ranges", "bytes")
	c.Header("Content-Disposition", "inline")
	//将MinIO中的内容转发到浏览器,拷贝部分内容
	_, err = io.CopyN(c.Writer, obj, length)
	if err != nil {
		c.JSON(500, gin.H{"error": "视频传输失败 " + err.Error()})
		return
	}
}

/*实现上传时断点续传：分片上传+最后合并*/

// 初始化上传任务
// 主要完成：【生成uploadId,设置分片大小，计算分片信息，存数据库，返回信息给前端】
// /upload/init 生成上传会话，分片信息
func InitUploadHandler(c *gin.Context) {

	/*1.定义请求格式体，前端JSON请求需包含file_name和total_size，
	从前端请求中获取对应的文件名和大小（后续填到session中），绑定到本地*/

	//定义局部变量[匿名结构体]req
	var req struct {
		FileName  string `json:"file_name" binding:"required"`
		TotalSize uint64 `json:"total_size" binding:"required"`
	}
	//c.ShouldBind解析前端发来的JSON请求,把前端里面的“file_name"和“total_size”映射到本地的req.FileName和req.TotalSize
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数错误"}) //400:Bad Request此处为客户端发的请求有问题
		return
	}

	/*2.初始化上传会话UploadSession，存入数据库中*/

	uploadId := uuid.New().String()                            //生成UploadId
	chunkSize := uint64(5 * 1024 * 1024)                       //5MB分片大小
	totalChunks := (req.TotalSize + chunkSize - 1) / chunkSize //上取整
	//从Gin上下文获取JWT放入的用户ID
	userIdAny, _ := c.Get("user_id")      //拿到的是interface{} ,里面装的是float64
	userId := uint64(userIdAny.(float64)) //userIdAny.(float64)把他从接口中取出来，uint64()再把float64转成我们需要的uint64
	//初始化UploadSession
	session := db.UploadSession{
		UploadId:     uploadId,
		UserId:       userId,
		FileName:     req.FileName,
		TotalSize:    req.TotalSize,
		ChunkSize:    chunkSize,
		TotalChunks:  totalChunks,
		UploadedSize: 0,
		Status:       "uploading",
	}
	database := db.GetDB() //获得数据库句柄
	//database.Create(&session)读取结构体字段，生成对应的INSERT语句，执行插入，写入数据库表
	//(并且把自增主键回填到 session.ID 字段中)
	if err := database.Create(&session).Error; err != nil {
		c.JSON(500, gin.H{"error": "创建上传会话失败"})
		return
	}

	/*3.初始化分片记录ChunkRecord*/

	//创建切片存储db.ChunkRecord类型的数据,长度为totalChunks
	chunkRecords := make([]db.ChunkRecord, totalChunks)
	//遍历切片，每次循环生成一条ChunkRecord
	for i := uint64(0); i < totalChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize - 1 //-1是因为文件的字节是从0开始计数
		//如果到最后一块了
		if end >= req.TotalSize {
			end = req.TotalSize - 1
		}
		//初始化每次的分片记录
		chunkRecords[i] = db.ChunkRecord{
			UploadId:   uploadId,
			ChunkIndex: i,
			Size:       end - start + 1, //[start,end],长度为end-start+1
			Status:     "pending",       //默认值，表示这个分片还没有上传
			StartByte:  start,
			EndByte:    end,
		}
	}

	//批量插入，一条sql插入多条记录
	if err := database.Create(&chunkRecords).Error; err != nil {
		c.JSON(500, gin.H{"error": "初始化分片记录失败"})
		return
	}

	/*4.初始化成功，返回响应*/
	c.JSON(200, gin.H{
		"upload_id":    uploadId,
		"chunk_size":   chunkSize,
		"total_chunks": totalChunks,
	})
}

// 执行分片上传
// 循环调用POST /upload/:uploadId/chunk/:index
func UploadChunkHandler(c *gin.Context) {
	//1.从URL路径中获取uploadId和块的index【保证和初始化的分片信息对应上】
	uploadId := c.Param("uploadId")
	indexStr := c.Param("index") //（HTTP请求里面的东西都是字符串）
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "分片编号错误"})
		return
	}

	//2.从请求表单中拿到该分片文件
	//gin从请求中找字段名为file的文件，返回一个*multipart.FileHeader 对象
	//对象里面包含：Filename上传时文件名，Size文件大小（字节数），Header额外的HTTP请求头信息
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"error": "未找到上传文件"})
		return
	}

	//打开文件
	src, _ := file.Open()
	defer src.Close()
	//拼出这片 分片 存到MinIO中的路径
	objectName := fmt.Sprintf("uploads/%s/chunk_%d", uploadId, index)

	//3.存储
	//把用户上传的分片数据流写到MinIO的指定路径,即上传分片到MinIO上
	_, err = minioClient.PutObject(
		c,          //Gin的*gin.Context，实现了context.Context 接口
		"videos",   //MinIO中的桶(Bucket)的名称
		objectName, //存储对象，此处即分片文件在MinIO中的路径
		src,        //数据来源，io.Reader,文件内容的流，MinIO从这里读数据
		file.Size,  //当前分片的数据大小（字节数）【注意：是实际收到的分片大小】
		//上传时的附加参数
		minio.PutObjectOptions{
			//把上传文件的MIME类型（如video/mp4)一并告诉MinIO
			ContentType: file.Header.Get("Content-Type"),
		},
	)
	if err != nil {
		c.JSON(500, gin.H{"error": "上传分片失败：" + err.Error()})
		return
	}
	//4.更新数据库
	//获得数据库句柄
	database := db.GetDB()
	tx := database.Begin() //开启事务
	defer func() {
		//recover()捕捉panic（运行时异常）,如果出现panic,事务回滚
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()
	//检查分片记录是否存在且已完成
	var existingChunk db.ChunkRecord
	//isNewChunk布尔值表示这个分片是否是新分片
	isNewChunk := tx.Where("upload_id=? AND chunk_index=?", uploadId, index).First(&existingChunk).Error != nil
	//判断是否是该分片的第一次有效完成
	isFirstTimeCompleted := isNewChunk || existingChunk.Status != "completed"

	//上传分片成功，更新数据库中的分片记录chunk_records
	if err := tx.Model(&db.ChunkRecord{}). //Model(&db.ChunkRecord{}):要更新的表是chunk_records
						Where("upload_id=? AND chunk_index=?", uploadId, index).
						Updates(map[string]interface{}{
			"status": "completed",
			"s3_key": objectName,
			"size":   file.Size,
		}).Error; err != nil {
		c.JSON(500, gin.H{"error": "更新分片记录失败"})
		return
	}
	//只有在首次有效完成时才累加大小（情况：新分片，补交分片计入进度。重复上传已完成分片只覆盖状态不会计入进度）
	if isFirstTimeCompleted {
		//更新数据库中的upload_sessions表中的已上传大小字段（upload_size)
		//Model(&db.UploadSession{})表示要操作upload_sessions这张表
		//gorm.Expr("表达式",参数)表示告诉GORM,不要把这个当做值，而是要当做表达式处理，
		//在这里用来累加已上传的字节数（【是实际收到的分片大小】）。
		//gorm.Expr()不用先查后加，避免并发条件出错
		if err := tx.Model(&db.UploadSession{}).
			Where("upload_id=?", uploadId).
			Update("uploaded_size", gorm.Expr("uploaded_size+?", file.Size)).Error; err != nil {
			tx.Rollback()
			c.JSON(500, gin.H{"error": "更新会话失败"})
			return
		}
	}
	tx.Commit()
	//5.响应消息
	c.JSON(200, gin.H{
		"message":     "分片上传成功",
		"chunk_index": index,
		"is_retry":    !isFirstTimeCompleted,
	})
}

// 完成分片上传
// POST /upload/<uploadId>/complete
// 查询所有分片，如果有未完成的，返回错误，如果全部完成，更新这个上传会话为完成
// 还要写MinIO Multipart API合并
func CompleteUploadHandler(c *gin.Context) {
	uploadId := c.Param("uploadId")
	database := db.GetDB()
	var count int64
	//检查状态是否全部完成
	//查询那些状态不为completed的,记数到count中
	if err := database.Model(&db.ChunkRecord{}).
		Where("upload_id=? AND status!=?", uploadId, "completed").
		Count(&count).Error; err != nil {
		c.JSON(500, gin.H{"error": "检查分片状态失败"})
		return
	}
	//count数量大于0，还有未完成的分片
	if count > 0 {
		c.JSON(400, gin.H{"error": "还有分片未完成"})
		return
	}

	//获取上传会话的信息【合并分片时需要知道文件名，写入video_infos时需要知道总大小，要确认这个会话真实存在】
	var session db.UploadSession
	//传入了 db.UploadSession 结构体，GORM 会自动把它映射到对应的表 upload_sessions 去查询
	if err := database.Where("upload_id=?", uploadId).First(&session).Error; err != nil {
		c.JSON(404, gin.H{"error": "上传会话不存在"})
		return
	}
	//获取所有分片，按顺序排列
	// 	SELECT *
	// FROM chunk_records
	// WHERE upload_id = '<uploadId>' AND status = 'completed'
	// ORDER BY chunk_index;
	var chunks []db.ChunkRecord
	database.Where("upload_id=? AND status=?", uploadId, "completed").Order("chunk_index").Find(&chunks)

	//合并分片
	err := mergeChunksToFinalFile(session.FileName, chunks)
	if err != nil {
		c.JSON(500, gin.H{"error": "合并分片失败 " + err.Error()})
		return
	}
	//创建最终的视频记录
	userIdAny, _ := c.Get("user_id")
	userId := uint64(userIdAny.(float64))
	videoInfo := db.VideoInfo{
		FileName:   session.FileName,
		Title:      session.FileName,
		Size:       int64(session.TotalSize),
		UploaderId: userId,
	}
	database.Create(&videoInfo)
	//清理分片文件
	go cleanupChunks(chunks)

	//更新上传会话表，将这个会话的状态改为已完成
	if err := database.Model(&db.UploadSession{}).
		Where("upload_id=?", uploadId).
		Update("status", "completed").Error; err != nil {
		c.JSON(500, gin.H{"error": "更新会话状态失败"})
		return
	}
	c.JSON(200, gin.H{"message": "文件上传完成",
		"filename": session.FileName})
}

// CompleteUploadHandler中用到的函数：合并分片
func mergeChunksToFinalFile(filename string, chunks []db.ChunkRecord) error {
	//创建根上下文
	ctx := context.Background()
	//获取文件的MIME类型
	contentType := getContentType(filename)
	//1.init-初始化Multipart Upload
	up, err := minioCore.NewMultipartUpload(
		ctx,
		"videos", //桶名
		filename, //最终对象名
		minio.PutObjectOptions{ContentType: contentType}, //上传选项
	)
	if err != nil {
		return fmt.Errorf("初始化multipart upload 失败：%w", err)
	}
	//标记上传是否完成
	completed := false
	//Abort
	defer func() {
		//如果没有完成，终止Multipart Upload
		if !completed {
			_ = minioCore.AbortMultipartUpload(ctx, "videos", filename, up)
		}
	}()

	//2.使用已经在MinIO上存在的分片
	completeParts := make([]minio.CompletePart, 0, len(chunks))
	for _, chunk := range chunks {
		//从已经存在的分片中获取数据流
		obj, err := minioClient.GetObject(ctx, "videos", chunk.S3Key, minio.GetObjectOptions{})
		if err != nil {
			return fmt.Errorf("获取分片%d失败：%w", chunk.ChunkIndex, err)
		}
		//使用PutObjectPart把现有分片复制到新的multipart upload中
		partRes, err := minioCore.PutObjectPart(ctx, "videos", filename, up, int(chunk.ChunkIndex+1), obj, int64(chunk.Size), minio.PutObjectPartOptions{})
		obj.Close()
		if err != nil {
			return fmt.Errorf("上传分片 %d 到myltipart失败：%w", chunk.ChunkIndex, err)
		}
		completeParts = append(completeParts, minio.CompletePart{
			PartNumber: int(chunk.ChunkIndex + 1),
			ETag:       partRes.ETag,
		})
	}

	//3.Complete
	_, err = minioCore.CompleteMultipartUpload(ctx, "videos", filename, up, completeParts, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("完成multipart Upload失败:%w", err)
	}
	completed = true
	return nil
}

// mergeChunksToFinalFile用到的辅助函数，用来判断文件类型
func getContentType(filename string) string {
	ext := filepath.Ext(filename)         //取文件后缀
	mimeType := mime.TypeByExtension(ext) //根据后缀检查MIME类型
	if mimeType == "" {
		return "application/octet-stream" //默认二进制流
	}
	return mimeType
}

// CompleteUploadHandler中用到的函数：异步清理分片文件
func cleanupChunks(chunks []db.ChunkRecord) {
	ctx := context.Background()
	for _, chunk := range chunks {
		err := minioClient.RemoveObject(ctx, "videos", chunk.S3Key, minio.RemoveObjectOptions{})
		if err != nil {
			fmt.Printf("清理分片失败：%s: %v\n", chunk.S3Key, err)
		}
	}
	fmt.Printf("已清理%d个分片文件\n", len(chunks))
}

// 查询上传进度
// GET /upload/:uploadId/progress
func GetUploadProgressHandler(c *gin.Context) {
	uploadId := c.Param("uploadId") //从请求URL中获取uploadId的值
	database := db.GetDB()
	//查询上传会话
	var session db.UploadSession
	if err := database.Where("upload_id=?", uploadId).First(&session).Error; err != nil {
		c.JSON(404, gin.H{"error": "上传会话不存在"})
		return
	}
	//查询已完成的分片
	var completedChunks []db.ChunkRecord
	database.Where("upload_id=? AND status=?", uploadId, "completed").Find(&completedChunks)
	//计算缺失分片
	completedMap := make(map[uint64]bool)
	for _, chunk := range completedChunks {
		completedMap[chunk.ChunkIndex] = true
	}
	var missingChunks []uint64
	for i := uint64(0); i < session.TotalChunks; i++ {
		if !completedMap[i] {
			missingChunks = append(missingChunks, i)
		}
	}
	//计算进度百分比
	progress := (float64(session.UploadedSize) / float64(session.TotalSize)) * 100
	//返回JSON
	c.JSON(200, gin.H{
		"upload_id":        uploadId,
		"status":           session.Status,
		"progress":         progress,
		"uploaded_size":    session.UploadedSize,
		"total_size":       session.TotalSize,
		"missing_chunks":   missingChunks,        //缺失的分片编号的切片，前端据此重传
		"completed_chunks": len(completedChunks), //已完成分片数
		"total_chunks":     session.TotalChunks,
	})
}
