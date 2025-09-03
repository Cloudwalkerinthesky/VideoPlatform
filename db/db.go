package db

//单独拆分出来，解决login.go和video.go循环引用的问题
import (
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// 定义一个全局的*gorm.DB变量(数据库连接句柄)
// 它是GORM提供数据库操作的入口，内部封装了连接池，事务，配置等内容
// 定义全局避免重复创建连接
var db *gorm.DB

func GetDB() *gorm.DB {
	return db
}

// 连接mysql数据库
func InitDB() {
	//用户名：密码@tcp(主机：端口)/数据库名？charset=utf8mb4&parseTime=True&loc=Local"
	dsn := "root:1234@tcp(127.0.0.1:3306)/go_project?charset=utf8mb4&parseTime=True&loc=Local" //DSN（data source name）
	var err error
	//连接数据库
	db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		panic("数据库连接失败: " + err.Error())
	}
	//自动迁移，检查有没有名为users,videoInfos,comments的表(默认表名为结构体小写加复数)
	//如果没有表，自动创建表。如果表已经存在，检查字段是否缺失，如果缺失则补上
	//不会删除已有字段，不会修改字段类型
	db.AutoMigrate(&User{}, &VideoInfo{}, &Comment{})
}

// gorm自动创建对应sql语句
type User struct {
	ID          uint64    `gorm:"primaryKey"` //映射为主键   //gorm会默认id的autoIncrement
	Name        string    `gorm:"unique;size:50"`
	Password    string    `gorm:"size:255"`
	CreatedTime time.Time `gorm:"autoCreateTime"`
}

type VideoInfo struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement"`
	FileName   string    `gorm:"uniqueIndex;size:150"` //存储在minIO中的名字 //建立唯一索引(非聚簇索引)
	Title      string    `gorm:"size:150"`             //可修改的展示性的视频标题  150字符以内
	Size       int64     //字节为单位
	UploadTime time.Time `gorm:"autoCreateTime"`
	UploaderId uint64    `gorm:"index"` //上传者的Id
}

type Comment struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement"`
	VideoId         uint64    `gorm:"not null" ` //日后可以建索引
	CommenterId     uint64    `gorm:"not null"`
	Content         string    `gorm:"type:varchar(1000)"` //1000字符以内 相当于"size:1000"
	CommentTime     time.Time `gorm:"autoCreateTime"`
	ParentCommentId uint64    //我想让它默认值为空,怎么弄？不管是不是就默认为空了？
	LikeCount       uint64    `gorm:"default:0"`
}
