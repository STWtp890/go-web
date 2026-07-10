package common

func RefreshTokenRedisKey(userId string) string {
	return "refresh_token:" + userId
}