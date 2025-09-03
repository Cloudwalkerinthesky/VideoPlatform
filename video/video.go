package video

import (
	"Project01/db"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var minioClient *minio.Client

// 初始化MinIO,返回MinIO客户端对象的指针和错误类型
func InitMinio() (*minio.Client, error) {
	endpoint := "localhost:9000"
	accessKeyID := "minioadmin"
	secretAccessKey := "minioadmin"
	useSSL := false
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}
	minioClient = client //赋值给全局变量
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
