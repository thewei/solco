# projectlibrary/
> L2 | 父级: /CLAUDE.md

成员清单
library.go: Solco 自有的 Project 资料库（projects-runtime P4，2026-10-31 批准）——`<userDataDir>/ProjectLibrary/<project>/{files/,manifest.json,INDEX.md}`；Add/AddFile/List/SetMode/Remove；三档 Always（≤4 KB 文本，名字进托管块）/Reference（只进 INDEX.md）/Task（只经新任务描述的 @path 行送达）；只传绝对路径、从不内联内容（含空白的路径写成 @"…"）；拒收凭证样文件、单文件 32 MB、每项目 100 项；项目名净化为 [A-Za-z0-9._-] 不能越出根（被净化改动过的名字追加原名 sha256 前 4 字节，"a b" 与 "a_b" 不共用书架）；Store 互斥锁串行化 Add/SetMode/Remove/WriteIndex；AddFile 对符号链接的目标也做凭证判定；Block/Splice 管理 `<!-- solco:library -->` 托管块，只替换块内、其余逐字节保留；TaskRefs 生成 “Context files:” 行
library_test.go: 增删改查、凭证/空/超大拒收、路径包含、Always 规则、块与索引内容、Splice 幂等、TaskRefs、AddFile 复制、路径引号、净化名不碰撞、符号链接指向凭证被拒、并发 Add 不丢项

法则: 无框架代码·不碰 ~/.herdr-projects·只给路径不给内容·日志不含文件内容
[PROTOCOL]: 变更时更新此头部，然后检查 /CLAUDE.md
