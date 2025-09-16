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
	db.AutoMigrate(&User{}, &VideoInfo{}, &Comment{},
		&Role{}, &Permission{}, &UserRole{}, &RolePermission{},
		&UploadSession{}, &ChunkRecord{})
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
	VideoId         uint64    `gorm:"not null;index" ` //建索引
	CommenterId     uint64    `gorm:"not null"`
	Content         string    `gorm:"type:varchar(1000)"` //1000字符以内 相当于"size:1000"
	CommentTime     time.Time `gorm:"autoCreateTime"`
	ParentCommentId uint64    //我想让它默认值为空,怎么弄？不管是不是就默认为空了？
	LikeCount       uint64    `gorm:"default:0"`
}

// 角色表
type Role struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement"`
	Name        string `gorm:"unique;size:50"` //1:user,2:admin,3:moderator
	Description string `gorm:"size:200"`
}

// 权限表
type Permission struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement"`
	Name        string `gorm:"unique;size:50"` //1:delete_comment,2:delete_video,3:delete_account
	Action      string `gorm:"size:50"`        //create,read,upload,delete
	Description string `gorm:"size:200"`
	Resource    string `gorm:"size:50"` //video,comment,account
}

// 用户-角色关联表
type UserRole struct {
	ID     uint64 `gorm:"primaryKey;autoIncrement"`
	UserId uint64 `gorm:"not null;index"`
	RoleId uint64 `gorm:"not null;index"`
}

// 角色-权限 关联表
type RolePermission struct {
	ID           uint64 `gorm:"primaryKey;autoIncrement"`
	RoleId       uint64 `gorm:"not null;index"`
	PermissionId uint64 `gorm:"not null;index"`
}

// 查询用户的 角色和权限 的函数
func GetUserRolesAndPermissions(userId uint64) (string, []string, error) {
	//SELECT r.name from user_roles ur
	// join roles r on r.id=ur.role_id
	// WHERE ur.user_id=xxx;
	var roleName string
	err := db.Table("user_roles").
		Select("r.name").
		Joins("JOIN roles r ON r.id=user_roles.role_id").
		Where("user_roles.user_id=?", userId).
		Pluck("name", &roleName).Error
	if err != nil {
		return "", nil, err
	}
	// SELECT DISTINCT p.name from user_roles ur
	// JOIN role_permissions rp ON ur.role_id=rp.role_id
	// JOIN permissions p ON p.id=rp.permission_id
	// WHERE ur.user_id=xxx;
	var permissionNames []string
	err = db.Table("user_roles").
		Select("DISTINCT p.name").
		Joins("JOIN role_permissions rp ON user_roles.role_id=rp.role_id").
		Joins("JOIN permissions p ON p.id=rp.permission_id").
		Where("user_roles.user_id=?", userId).
		Pluck("name", &permissionNames).Error
	if err != nil {
		return "", nil, err
	}
	return roleName, permissionNames, nil
}

// 给用户分配默认角色 user
func AssignDefaultRole(userId uint64) error {
	var userRoles Role //存放查找结果
	//在角色表中查找名字为user的角色的那行数据 ,填充到userRoles这个结构体中
	//SELECT * FROM roles WHERE name='user' LIMIT 1;
	err := db.Where("name=?", "user").First(&userRoles).Error
	if err != nil {
		return err
	}
	//构建一条 用户-角色表 的新记录
	userRoleAssignment := UserRole{
		UserId: userId,
		RoleId: userRoles.ID,
	}
	//INSERT INTO user_roles (user_id,role_id) VALUES (?,?);
	return db.Create(&userRoleAssignment).Error //插入这条新映射到用户-角色表中
}

// 实现上传时断点续传

// 上传会话表
type UploadSession struct {
	ID           uint64 `gorm:"primaryKey;autoIncrement"`
	UploadId     string `gorm:"uniqueIndex;size:100"` //UUID，标识一次上传任务（创建唯一索引）
	UserId       uint64 `gorm:"index"`                //用户ID,创建索引
	FileName     string `gorm:"size:255"`
	TotalSize    uint64 //文件总大小
	ChunkSize    uint64 `gorm:"default:5242880"` //分片大小默认5MB=5*1024*1024字节
	TotalChunks  uint64 //总分片数
	UploadedSize uint64 //已上传大小
	Status       string `gorm:"size:20;default:'uploading'"` //uploading,completed,failed
	//创建时间，记录这个上传任务什么时候开始的
	CreatedTime time.Time `gorm:"autoCreateTime"`
	//更新时间：每次上传一个分片，都会更新UploadedSize,UpdatedTime会自动刷新，
	//表示最新进度更新时间，可以用它来判断上传是不是还在进行
	UpdatedTime time.Time `gorm:"autoUpdateTime"`
}

// 分片记录表
type ChunkRecord struct {
	ID uint64 `gorm:"primaryKey;autoIncrement"`
	//给UploadId和ChunkIndex分别建各自的索引
	//再建立一个(UploadId,ChunkIndex)联合索引
	UploadId   string `gorm:"index;uniqueIndex:idx_upload_chunk;size:100"` //UUID，上传会话的唯一标识，用来标明这个分片属于哪个会话
	ChunkIndex uint64 `gorm:"index;uniqueIndex:idx_upload_chunk"`          //分片编号

	Size        uint64    //分片大小
	Status      string    `gorm:"default:pending"` //pending/uploading/completed/failed
	S3Key       string    //分片在MinIO中的存储路径
	StartByte   uint64    //分片起始字节
	EndByte     uint64    //分片结束字节
	Checksum    string    //校验和(MD5/SHA-256)
	CreatedTime time.Time `gorm:"autoCreateTime"`
}
