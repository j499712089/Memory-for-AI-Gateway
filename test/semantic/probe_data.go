package semantic

// ProbeData contains text pairs for threshold calibration.
// Synonymous pairs should have high cosine similarity (same meaning, different wording).
// Unrelated pairs should have low cosine similarity (completely different topics).

type TextPair struct {
	A string
	B string
}

// EnglishSynonymous contains English text pairs with similar semantic meaning
var EnglishSynonymous = []TextPair{
	{"how to reset my password", "password reset instructions"},
	{"database migration guide", "db migration handbook"},
	{"configure SSL certificate", "SSL certificate setup instructions"},
	{"user authentication failed", "authentication error for user"},
}

// EnglishUnrelated contains English text pairs with completely different meanings
var EnglishUnrelated = []TextPair{
	{"quantum chromodynamics", "banana bread recipe"},
	{"database connection pool", "the weather in Paris is mild"},
	{"configure SSL certificate", "weekend hiking trip planning"},
	{"memory retrieval interface", "chocolate cake ingredients"},
}

// ChineseSynonymous contains Chinese text pairs with similar semantic meaning
var ChineseSynonymous = []TextPair{
	{"如何重置我的密码", "密码重置说明"},
	{"数据库连接配置", "数据库连接池设置"},
	{"团队记忆检索接口", "检索团队记忆的接口"},
	{"用户身份验证失败", "用户认证错误"},
}

// ChineseUnrelated contains Chinese text pairs with completely different meanings
var ChineseUnrelated = []TextPair{
	{"量子色动力学", "香蕉面包食谱"},
	{"数据库连接配置", "巴黎今天天气温和"},
	{"网关鉴权失败", "周末去海边野餐"},
	{"团队记忆检索", "巧克力蛋糕配料"},
}
