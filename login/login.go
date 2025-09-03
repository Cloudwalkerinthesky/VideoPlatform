package login

import (
	"Project01/db"
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// 用于接受客户端的登录请求
type LoginRequest struct {
	UserName string `json:"username" binding:"required"` //用户名：必填
	Password string `json:"password" binding:"required"` //密码：必填
}

// 返回给客户端的登录结果
type Reply struct {
	UserName  string `json:"username"`
	UserId    uint64 `json:"user_id"`
	Token     string `json:"token"`      //JWT令牌
	ExpiresAt int64  `json:"expires_at"` //令牌过期时间-UNIX时间戳
	Message   string `json:"message"`    //响应信息
}

// 定义JWT密钥
var jwtKey = []byte("kirakira_dokidoki")

// 生成JWT token
func generateToken(user db.User) (string, int64, error) {
	expirationTime := time.Now().Add(time.Hour * 2).Unix()
	//创建claims
	claims := jwt.MapClaims{
		"user_id":  user.ID,
		"username": user.Name,
		"exp":      expirationTime,
	}
	//创建token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(jwtKey)
	//header为{"alg":"HS256","typ":"JWT"}
	//payload为我写的claims(包含user_id,username,exp)
	//signature为对前两段用密钥加密后生成的哈希值
	return signedToken, expirationTime, err
}

// 登录处理函数
func LoginHandler(c *gin.Context) {
	//定义一个LoginRequest变量
	var req LoginRequest
	//接受前端传来的LoginRequest（json形式）
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数错误"})
		return
	}
	//用户名是req.UserName ,密码是req.Password
	var user db.User
	//获取数据库连接句柄
	database := db.GetDB()
	//查询用户是否存在，并且把数据库中对应的信息填到user结构体变量里面
	if err := database.Where("name=?", req.UserName).First(&user).Error; err != nil {

		//如果用户不存在，就创建它
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
			if err != nil {
				c.JSON(500, gin.H{"error": "密码加密失败"})
				return
			}
			//创建新用户
			user = db.User{
				Name:     req.UserName,
				Password: string(hashedPassword),
			}
			if err := database.Create(&user).Error; err != nil {
				c.JSON(500, gin.H{"error": "创建用户失败"})
				return
			}

		} else {
			//其他查询错误
			c.JSON(500, gin.H{"error": "数据库查询错误"})
			return

		}
	}
	//相当于
	//执行SQL查询 SELECT * FROM users WHERE name = req.UserName LIMIT 1,把结果填到user变量里面
	// result:=db.Where("name=?",req.UserName).First(&user)
	// err:=result.Error;
	// if err!=nil{
	// 	c.JSON(401,gin.H{"error":"用户名不存在"})
	// 	return
	// }

	//比较密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		c.JSON(401, gin.H{"error": "密码错误"})
		return
	}
	token, expiresAt, err := generateToken(user)
	if err != nil {
		c.JSON(500, gin.H{"error": "生成令牌失效"})
		return
	}
	//返回给客户端
	c.JSON(200, Reply{
		UserName:  user.Name,
		UserId:    user.ID,
		Token:     token,
		ExpiresAt: expiresAt,
		Message:   "登录成功",
	})

}

// JWT鉴权中间件 Gin的中间件机制需要一个返回 Handler函数 的函数(工厂函数)
// 这个函数把中间件逻辑包成一个可以被注册的函数对象，而不是立即执行
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		//从前端请求头中拿到请求的token字符串
		tokenString := c.GetHeader("Authorization")
		//如果解析到的tokenString为空或者不以"Bear "开头
		if tokenString == "" || !strings.HasPrefix(tokenString, "Bearer ") {
			c.JSON(401, gin.H{"error": "缺少Token"})
			c.Abort() //停止当前请求的整个处理链
			return
		}
		tokenString = strings.TrimPrefix(tokenString, "Bearer ")
		claims := jwt.MapClaims{}
		//函数：输入token,输出密钥
		keyFunction := func(token *jwt.Token) (interface{}, error) {
			//interface{}为任意类型
			return jwtKey, nil
		}
		//验证
		token, err := jwt.ParseWithClaims(tokenString, claims, keyFunction)
		if err != nil || !token.Valid {
			if errors.Is(err, jwt.ErrTokenExpired) {
				c.JSON(401, gin.H{"error": "Token已过期"})
			} else {
				c.JSON(401, gin.H{"error": "无效的Token"})
			}
			c.Abort()
			return
		}
		//把解析出来的用户名和用户id放到上下文中
		c.Set("username", claims["username"])
		c.Set("user_id", claims["user_id"])
		c.Next()
	}
}

//目前表现数据
//[21.022ms] [rows:0] SELECT * FROM `users` WHERE name='Jack' ORDER BY `users`.`id` LIMIT 1
// [GIN] 2025/07/30 - 23:38:24 | 200 |    225.1539ms |             ::1 | POST     "/login"
// [GIN] 2025/07/30 - 23:38:49 | 200 |     76.0095ms |             ::1 | POST     "/login"
// [GIN] 2025/07/30 - 23:39:01 | 401 |      69.786ms |             ::1 | POST     "/login"

// 2025/07/30 23:39:57 D:/GoProjects-New/Project01/login/main.go:91 record not found
// [0.507ms] [rows:0] SELECT * FROM `users` WHERE name='JOJO' ORDER BY `users`.`id` LIMIT 1
// [GIN] 2025/07/30 - 23:39:58 | 200 |    160.7087ms |             ::1 | POST     "/login"
// [GIN] 2025/07/30 - 23:40:06 | 200 |     75.1266ms |             ::1 | POST     "/login"
// [GIN] 2025/07/30 - 23:40:43 | 200 |     70.2674ms |             ::1 | POST     "/login"
// [GIN] 2025/07/30 - 23:40:44 | 200 |     76.3041ms |             ::1 | POST     "/login"
// [GIN] 2025/07/30 - 23:40:45 | 200 |     79.0592ms |             ::1 | POST     "/login"
// [GIN] 2025/07/30 - 23:40:46 | 200 |     73.3773ms |             ::1 | POST     "/login"

// 2025/07/30 23:42:27 D:/GoProjects-New/Project01/login/main.go:91 record not found
// [1.207ms] [rows:0] SELECT * FROM `users` WHERE name='Kira' ORDER BY `users`.`id` LIMIT 1
// [GIN] 2025/07/30 - 23:42:27 | 200 |    194.7084ms |             ::1 | POST     "/login"
// [GIN] 2025/07/30 - 23:42:29 | 200 |     88.4936ms |             ::1 | POST     "/login"
