# 认证逻辑的设计

## JWT

### 总体策略

| Token 类型 | 策略 | 原则 |
|-----------|------|------|
| Access Token | **黑名单** | 有效的 token 不存 Redis, 只存被吊销的 |
| Refresh Token | **白名单** | 只有 Redis 中存在的 token 才有效 |

### Redis Key 规范

#### Access Token 黑名单

全语义: `jwt:accessToken:blacklist:{accessTokenId}`  
简化:   `jwt:acs:bl:{accessTokenId}`  
Value:  "1"  
TTL:    = accessToken 剩余有效期（精确计算, 上限保护）  

#### Refresh Token 白名单

全语义: `jwt:refreshToken:whitelist:{userId}:{refreshTokenId}`  
简化:   `jwt:ref:wl:{userId}:{refreshTokenId}`  
Value:  SHA256(token) 前 16 位 hex  
TTL:    = RefreshExpireHours  

---

## 四种场景的 Redis 行为

### 1. 登录 (POST /login)

```go
func LoginLogic() {
    bcrypt.CompareHashAndPassword() // ← 验证密码
    signToken(accessToken,  AccessExpireHours)   // ← RS256 签发
    signToken(refreshToken, RefreshExpireHours)  // ← RS256 签发

    // 不写 "有效 token 不需要存, 只在吊销时才写"
    RedisAccessToken()

    // 写入, SET jwt:ref:wl:{uid}
    // Value = SHA256(refreshToken) 前 16 位 hex
    // TTL   = RefreshExpireHours
    // 不影响 Access Token
    RedisRefreshToken()
}
```

### 2. 刷新 (POST /refresh-token)

```go
func RefreshTokenLogic() {
    // ← RS256 验签
    jwt.Parse(oldRefreshToken, publicKey)// 提取 email, userID from claims

    // GET jwt:ref:wl:{uid}
    // value ≠ hash(oldToken) → 拒绝（已被轮换或吊销）
    RedisRefreshToken()

    // SET jwt:acs:bl:{hash(旧AT)}
    // 可选：如果旧 AT 仍在有效期内, 加入黑名单防止被继续使用
    Redis Access Token()

    signToken(newAccessToken,  AccessExpireHours)   // ← 签发新 AT
    signToken(newRefreshToken, RefreshExpireHours)  // ← 签发新 RT（轮换）

    // SET jwt:ref:wl:{uid}
    // Value = SHA256(newRefreshToken)
    // 覆盖旧值（Write Through）
    RedisRefreshToken() 
    
    
    // 效果: 旧 Refresh Token 被覆盖 → 立即失效, 无法再用于刷新
}
         
```

### 3. 登出 (POST /logout)

```go
func AuthRequired() { // 中间件（先验证身份）
    RS256 jwt.Parse()                     ← 验签
    claims → context
}

func LogoutHandler() {
    // SET jwt:acs:bl:{hash(AT)}
    // TTL = AT 剩余有效时间（精确计算 + 上限保护）
    // 此 AT 立即失效, 中间件下次检查时返回 401
    RedisAccessToken()

    // DEL jwt:ref:wl:{uid}
    // "强制下线, 无法再刷新"
    RedisRefreshToken()
}
```

### 4. 每次请求（AuthRequired 中间件）

```go
func AuthRequired() {
    extractToken("Bearer xxx")  // ← 提取
    jwt.Parse(token, publicKey) // ← RS256 验签

    // EXISTS jwt:acs:bl:{hash(AT)}
    // 存在 → 401（token 已被吊销）
    // 不存在 → ✅ 放行
    RedisAccessToken()

    // 不查
    // Refresh Token 只在刷新接口中使用
    RedisRefreshToken()

    c.Set("claims", jwtToken.Claims) // ← 注入用户信息
}
```

---

## Redis 操作次数汇总

| 场景 | 读 | 写 |
|------|----|----|
| 登录 | 0 | 1 SET refresh |
| 刷新 | 1 GET refresh | 2 SET bl(可选) + SET refresh |
| 登出 | 0 | 2 SET bl + DEL refresh |
| 每次请求 | 1 EXISTS bl | 0 |

---

## 设计目的

1. **无感登录续期**

    通过 Refresh Token 刷新防止 Access Token 到期强制重新登录

2. **减少 Redis 写入**

    Access Token 仅在吊销（登出/封禁）时写, 99% 请求不写 Redis

3. **减少内存占用**

    正常 Access Token 不占 Redis 内存, 黑名单条目 TTL 自动过期

4. **可随时吊销**

    删除 Refresh Token 白名单 → 用户下线, 无法刷新

5. **支持多设备管理**

    当前 Refresh Token 白名单按 `{userId}` `{refreshTokenId}` 账户粒度管理登录;  
    或后续升级`{userId}:{jti}` `{refreshTokenId}` 设备粒度登录管理.  
