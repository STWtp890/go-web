package auth

// 响应消息常量
const (
	MsgLoginSuccess          = "Login successful"
	MsgRegisterSuccess       = "Success Registered!"
	MsgTokenRefreshed        = "Token refreshed successfully"
	MsgInvalidCredentials    = "Invalid email/userId or password"
	MsgIncorrectPassword     = "Incorrect password"
	MsgNoRoleAssigned        = "User has no assigned role. Please contact the administrator."
	MsgMissingUserId         = "invalid token: missing userId"
	MsgMissingRoleId         = "Invalid token: missing roleId"
	MsgTokenValidationFailed = "refresh token validation failed"
	MsgTokenInvalidOrUsed    = "refresh token invalid or already used"
	MsgGenAccessTokenFailed  = "Failed to generate access token"
	MsgGenRefreshTokenFailed = "Failed to generate refresh token"
	MsgRedisSetFailed        = "Failed to set refresh token in Redis"
	MsgRedisUpdateFailed     = "Failed to update refresh token in Redis"
	MsgProcessPwdFailed      = "Failed to process password"
	MsgInsertUserFailed      = "Failed to insert user"
	MsgGetUserIdFailed       = "Failed to get user ID"
	MsgFindDefaultRoleFailed = "Failed to find default role"
	MsgAssignDefaultRoleFail = "Failed to assign default role"
)