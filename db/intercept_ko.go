package db

// Translate only untouched built-in defaults. Exact old name/message matching
// keeps user-edited rules and their enabled/order settings intact.
func (d *DB) localizeDefaultInterceptRulesKorean() error {
	type pair struct{ oldName, oldMessage, newName, newMessage string }
	rules := []pair{
		{"[内置] 递归强制删除 rm -rf", "禁止执行递归强制删除（rm -rf / rm --recursive），可能永久损坏系统或靶机环境", "[기본 제공] 강제 재귀 삭제 rm -rf", "강제 재귀 삭제(rm -rf / rm --recursive)는 시스템이나 실습 환경을 영구적으로 손상할 수 있어 금지됩니다"},
		{"[内置] 删除系统关键目录", "禁止删除系统关键路径", "[기본 제공] 시스템 주요 디렉터리 삭제", "시스템 주요 경로는 삭제할 수 없습니다"},
		{"[内置] 磁盘格式化 mkfs", "禁止格式化磁盘（mkfs）", "[기본 제공] 디스크 포맷 mkfs", "디스크 포맷(mkfs)은 금지됩니다"},
		{"[内置] 覆写磁盘设备 dd", "禁止使用 dd 覆写磁盘设备", "[기본 제공] dd로 디스크 장치 덮어쓰기", "dd를 이용한 디스크 장치 덮어쓰기는 금지됩니다"},
		{"[内置] Fork 炸弹", "禁止执行 Fork 炸弹", "[기본 제공] 포크 폭탄", "포크 폭탄 실행은 금지됩니다"},
		{"[内置] 关机 / 重启", "禁止执行关机或重启命令", "[기본 제공] 종료 / 재시작", "종료 또는 재시작 명령은 금지됩니다"},
		{"[内置] 杀死全部进程", "禁止 kill -9 -1 或 killall -9（杀死所有进程）", "[기본 제공] 모든 프로세스 종료", "모든 프로세스를 종료하는 kill -9 -1 또는 killall -9는 금지됩니다"},
		{"[内置] 磁盘擦除 shred / wipe", "禁止对磁盘设备执行 shred/wipe 擦除", "[기본 제공] 디스크 삭제 shred / wipe", "디스크 장치에 shred/wipe를 실행할 수 없습니다"},
		{"[内置] 清空防火墙规则", "禁止清空防火墙规则（iptables -F / nft flush）", "[기본 제공] 방화벽 규칙 전체 삭제", "방화벽 규칙 전체 삭제(iptables -F / nft flush)는 금지됩니다"},
		{"[内置] SQL DROP DATABASE / TABLE / SCHEMA", "禁止执行 DROP 操作，可能不可逆地销毁数据库对象", "[기본 제공] SQL DROP DATABASE / TABLE / SCHEMA", "DROP 작업은 데이터베이스 객체를 되돌릴 수 없게 삭제할 수 있어 금지됩니다"},
		{"[内置] SQL TRUNCATE", "禁止执行 TRUNCATE，可能清空数据表所有数据", "[기본 제공] SQL TRUNCATE", "TRUNCATE는 테이블의 모든 데이터를 비울 수 있어 금지됩니다"},
		{"[内置] MongoDB drop / dropDatabase", "禁止执行 MongoDB drop 操作", "[기본 제공] MongoDB drop / dropDatabase", "MongoDB drop 작업은 금지됩니다"},
		{"[内置] Redis FLUSHALL / FLUSHDB", "禁止执行 Redis FLUSHALL / FLUSHDB，可能清空全部缓存数据", "[기본 제공] Redis FLUSHALL / FLUSHDB", "Redis FLUSHALL / FLUSHDB는 모든 캐시 데이터를 비울 수 있어 금지됩니다"},
		{"[内置] curl / wget 发送 DELETE 请求", "禁止通过 curl/wget 发送 HTTP DELETE 请求，可能删除目标系统数据", "[기본 제공] curl / wget DELETE 요청", "curl/wget의 HTTP DELETE 요청은 대상 데이터 삭제를 유발할 수 있어 금지됩니다"},
		{"[内置] Python HTTP 客户端 DELETE（requests/httpx/aiohttp）", "禁止使用 Python HTTP 客户端发送 DELETE 请求", "[기본 제공] Python HTTP 클라이언트 DELETE(requests/httpx/aiohttp)", "Python HTTP 클라이언트의 DELETE 요청은 금지됩니다"},
		{"[内置] 脚本中声明 HTTP DELETE 方法（JS/通用）", "禁止在脚本中声明并发送 HTTP DELETE 请求", "[기본 제공] 스크립트의 HTTP DELETE 메서드(JS/일반)", "스크립트에서 HTTP DELETE 요청을 지정하고 전송할 수 없습니다"},
		{"[内置] 批量清空 / 清除接口路径", "禁止调用批量清空或销毁类接口（/clear /wipe /flush /purge 等）", "[기본 제공] 대량 삭제 엔드포인트 경로", "대량 삭제 또는 파괴 엔드포인트(/clear /wipe /flush /purge 등)는 호출할 수 없습니다"},
		{"[内置] 破坏性系统命令", "破坏性命令被拒绝（rm -rf / / mkfs / dd / fork bomb / 关机重启 / 覆写磁盘设备）", "[기본 제공] 파괴적인 시스템 명령", "파괴적인 명령이 거부되었습니다(rm -rf /, mkfs, dd, 포크 폭탄, 종료/재시작, 디스크 장치 덮어쓰기)"},
		{"[内置] 数据外泄管道", "疑似数据外泄管道被拒绝（命令输出经 curl/wget/nc 外传）", "[기본 제공] 데이터 외부 전송 파이프라인", "데이터 외부 전송으로 의심되는 파이프라인이 거부되었습니다(명령 출력을 curl/wget/nc로 전송)"},
		{"[内置] 删除类接口路径", "禁止调用删除类接口（/delete /remove /unlink /erase 等），不论使用哪种 HTTP 方法——多数应用的删除接口用 GET/POST 就能触发，同样会真实删除目标数据", "[기본 제공] 삭제 엔드포인트 경로", "HTTP 메서드와 관계없이 삭제 엔드포인트(/delete /remove /unlink /erase 등)를 호출할 수 없습니다. 많은 애플리케이션에서 GET/POST로도 실제 데이터가 삭제됩니다"},
	}
	for _, r := range rules {
		if _, err := d.Exec(`UPDATE intercept_rules SET name=$3, message=$4 WHERE name=$1 AND message=$2`, r.oldName, r.oldMessage, r.newName, r.newMessage); err != nil {
			return err
		}
	}
	assets := []struct{ oldNote, newNote string }{
		{"[内置] 政府网站 (.gov)", "[기본 제공] 정부 사이트 (.gov)"},
		{"[内置] 政府网站 (.gov.cn)", "[기본 제공] 정부 사이트 (.gov.cn)"},
		{"[内置] 教育网站 (.edu)", "[기본 제공] 교육 사이트 (.edu)"},
		{"[内置] 教育网站 (.edu.cn)", "[기본 제공] 교육 사이트 (.edu.cn)"},
	}
	for _, a := range assets {
		if _, err := d.Exec(`UPDATE asset_intercept_rules SET note=$2 WHERE builtin=true AND note=$1`, a.oldNote, a.newNote); err != nil {
			return err
		}
	}
	return nil
}
