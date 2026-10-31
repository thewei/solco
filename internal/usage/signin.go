package usage

/**
 * [INPUT]: 调用方注入的凭据缓存（生产为 mygo.Secrets，测试为内存实现）
 * [OUTPUT]: SigninCache 接口
 * [POS]: 借用的他人登录经同意读取一次后，把凭据副本存进 Solco 自己的凭据条目（nativeui 侧 key 为 usage.<id>.signin）；之后的读取只碰副本，不再触碰对方应用的 Keychain 条目、不再触发 macOS 询问；副本失效（过期、损坏、API 拒绝）即删除，下次读取重新取源
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// SigninCache keeps a provider's borrowed sign-in in Solco's own credential
// store: the other app's Keychain item — whose read makes macOS ask — is
// touched only while no copy exists or the copy was rejected. Production is
// mygo.Secrets; tests inject memory.
type SigninCache interface {
	Get(key string) (string, bool)
	Set(key, value string) error
	Delete(key string) error
}
