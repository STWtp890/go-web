# 用户认证与单有效会话

## Token

用户登录后签发一对 RS256 JWT：

| 类型 | `token_use` | 有效期 | 用途 |
| --- | --- | --- | --- |
| Access Token | `access` | `access_expire_hours` | 访问受保护接口 |
| Refresh Token | `refresh` | `refresh_expire_hours` | 原子轮换并取得新的 Token 对 |

两类 Token 都包含 `iss`、`sub`、`sid`、`jti`、`iat`、`nbf` 与 `exp`。`sid` 是一次登录会话的随机标识；`jti` 是每个 JWT 独有的标识。

## Redis 会话状态

每个普通用户只保存一个当前有效会话：

```text
jwt:session:user:<uid>  (Hash, TTL = Refresh Token 有效期)
  sid           <当前会话 ID>
  refresh_hash  <SHA-256(当前 Refresh Token)>
```

登录会以 Lua 脚本原子覆盖该 Hash；因此后一次成功登录会替换旧 `sid` 和旧 Refresh Token 哈希。

## 请求鉴权

`AuthRequired` 的处理顺序：

1. 从 `pp_user_at` HttpOnly Cookie 提取 Access Token。
2. 固定接受 RS256，校验签名、签发者、有效期和 `token_use=access`。
3. 从 claims 提取 `sub` 和 `sid`。
4. 读取 `jwt:session:user:<sub>`，只有其中的 `sid` 等于 Token 的 `sid` 才放行。

所以新登录、主动登出或未来的管理员强制下线删除/覆盖会话后，旧 Access Token 不需要逐条写入黑名单，也会在下一次受保护请求时立即返回 401。Redis 不可用时鉴权失败关闭，返回 503。

## 刷新

刷新接口验签旧 Refresh Token 后，使用 Lua 原子确认：

- Token `sid` 仍为该用户当前 `sid`；
- `refresh_hash` 仍匹配旧 Refresh Token。

通过后才写入新 Refresh Token 哈希，并延长会话 TTL。并发使用同一个 Refresh Token 时最多一个请求成功。

## 登出

登出只在 Access Token 中的 `sid` 仍等于 Redis 当前 `sid` 时删除会话 Hash。这个条件删除避免旧端延迟发出的登出请求误删新端的会话。

## 当前范围

本文件描述普通用户 `auth` 链路；管理员在 `service/manager` 中使用独立 Cookie 与等价的 `sid` 会话校验。登录替换旧 SID 和成功登出会向 Redis Pub/Sub 写入 `session.revoked` 事件；Token 有效性仍以 Redis 会话状态为准。所有受保护 HTTP 与 WebSocket 路由均先经 Cookie JWT 鉴权。
