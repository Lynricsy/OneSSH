#!/usr/bin/env python3
"""重新生成卡片预览画廊用的样例数据。

样例里的时间戳按「相对现在」生成，这样画廊里的「3 小时前」「2 天前」才是自然的；
时间久了想让预览重新贴近当下，直接重跑一次这个脚本即可：

    python3 internal/mcpserver/apps/preview/fixtures/generate.py

数据本身是照着各工具真实的 structuredContent 结构手写的，覆盖了成功、截断、
非零退出码、超时、离线主机、软链接、缺失字段等实际会遇到的形态。
"""

import json, os, time

NOW = int(time.time())
def ago(seconds): return NOW - seconds
MIN, HOUR, DAY = 60, 3600, 86400

def sc(obj, text=None):
    return {"content":[{"type":"text","text": text if text is not None else json.dumps(obj, ensure_ascii=False, indent=2)}],
            "structuredContent": obj, "isError": False}

F = {}

hosts = [
 {"name":"web-01","addr":"10.0.1.11:22","username":"deploy","online":True,"tags":["prod","nginx"]},
 {"name":"web-02","addr":"10.0.1.12:22","username":"deploy","online":True,"tags":["prod","nginx"]},
 {"name":"db-main","addr":"10.0.2.10:22","username":"postgres","online":True,"tags":["prod","postgres"]},
 {"name":"cache-01","addr":"10.0.2.21:22","username":"redis","online":False,"tags":["prod","redis"]},
 {"name":"build-runner","addr":"192.168.30.7:2222","username":"ci","online":True,"tags":["staging","ci"]},
 {"name":"bastion","addr":"203.0.113.9:22","username":"jump","online":True,"tags":[]},
]
F["hosts_list"] = {"input":{}, "result": sc({"hosts":hosts})}

managed = [
 {"id":1,"name":"web-01","addr":"10.0.1.11","port":22,"username":"deploy","auth_type":"key","key_id":3,
  "hostkey_fp":"SHA256:8Qk1v0oR2mZpN4xTfLcYs7WbJdEuHgAiKq3RnVe5Xy0","jump_host":"bastion","monitor_enabled":True,
  "tags":["prod","nginx"],"created_at":ago(97*DAY),"online":True},
 {"id":2,"name":"web-02","addr":"10.0.1.12","port":22,"username":"deploy","auth_type":"key","key_id":3,
  "hostkey_fp":"SHA256:pR7dLm2Qw9XcVb1NkFgYt6HsJuAeZoIx4Cn0RvUy8Tq","jump_host":"bastion","monitor_enabled":True,
  "tags":["prod","nginx"],"created_at":ago(97*DAY-120),"online":True},
 {"id":3,"name":"db-main","addr":"10.0.2.10","port":22,"username":"postgres","auth_type":"key","key_id":4,
  "hostkey_fp":"SHA256:Zx4Cv8Bn2Mq7Kj1Hg5Fd9Sa3Pl6Wo0Ei7Ru2Ty4Ui8Op","jump_host":None,"monitor_enabled":True,
  "tags":["prod","postgres"],"created_at":ago(112*DAY),"online":True},
 {"id":4,"name":"cache-01","addr":"10.0.2.21","port":22,"username":"redis","auth_type":"password","key_id":None,
  "hostkey_fp":None,"jump_host":None,"monitor_enabled":False,"tags":["prod","redis"],"created_at":ago(63*DAY),"online":False},
 {"id":5,"name":"build-runner","addr":"192.168.30.7","port":2222,"username":"ci","auth_type":"key","key_id":7,
  "hostkey_fp":"SHA256:Nm3Bv6Cx9Zl2Ka5Jh8Gf1Ds4Ap7Qw0Er3Ty6Ui9Op2Ls","jump_host":None,"monitor_enabled":True,
  "tags":["staging","ci"],"created_at":ago(41*DAY),"online":True},
]
F["hosts_manage_list"] = {"input":{}, "result": sc({"hosts":managed})}
F["host_create"] = {"input":{"name":"cache-02","addr":"10.0.2.22","port":22,"username":"redis","auth_type":"key","key_id":3,"tags":["prod","redis"]},
                    "result": sc({"id":9,"name":"cache-02","addr":"10.0.2.22","port":22,"username":"redis","auth_type":"key",
                                  "key_id":3,"hostkey_fp":None,"jump_host":None,"monitor_enabled":True,"tags":["prod","redis"],
                                  "created_at":ago(2*MIN),"online":False})}
F["host_update"] = {"input":{"host":"cache-01","name":"cache-01","addr":"10.0.2.21","port":22,"username":"redis",
                             "auth_type":"key","key_id":3,"monitor_enabled":True,"tags":["prod","redis","tls"]},
                    "result": sc(dict(managed[3], auth_type="key", key_id=3, monitor_enabled=True, tags=["prod","redis","tls"]))}
uptime_out = " 14:23:07 up 42 days,  3:11,  2 users,  load average: 0.34, 0.51, 0.48"
F["host_test"] = {"input":{"host":"web-01"},
                  "result": sc({"stdout":uptime_out+"\n","stderr":"","output":uptime_out,"exit_code":0,
                                "cwd":"/home/deploy","timeout":False,"truncated":False,"total_lines":1,"total_bytes":78,
                                "stdout_bytes":78,"stderr_bytes":0,"output_recorded":True})}
F["host_reset_fingerprint"] = {"input":{"host":"cache-01"}, "result": sc({"ok":True})}
F["host_delete"] = {"input":{"host":"cache-01"}, "result": sc({"ok":True})}

head = [
 "● nginx.service - A high performance web server and a reverse proxy server",
 "     Loaded: loaded (/lib/systemd/system/nginx.service; enabled; preset: enabled)",
 "     Active: active (running) since Mon 2026-08-25 09:12:44 UTC; 8 days ago",
 "       Docs: man:nginx(8)",
 "   Main PID: 1183 (nginx)",
 "      Tasks: 9 (limit: 9451)",
 "     Memory: 24.8M (peak: 31.2M)",
 "        CPU: 4min 51.203s",
 "     CGroup: /system.slice/nginx.service",
 "             ├─1183 \"nginx: master process /usr/sbin/nginx -g daemon on; master_process on;\"",
 "             ├─1184 \"nginx: worker process\"",
 "             └─1185 \"nginx: worker process\"",
 "",
 "Sep 02 09:00:01 web-01 systemd[1]: Reloading A high performance web server...",
 "Sep 02 09:00:01 web-01 nginx[28841]: nginx: the configuration file /etc/nginx/nginx.conf syntax is ok",
 "Sep 02 09:00:01 web-01 nginx[28841]: nginx: configuration file /etc/nginx/nginx.conf test is successful",
 "Sep 02 09:00:01 web-01 systemd[1]: Reloaded A high performance web server.",
]
tail_lines = []
for n in range(30):
    tail_lines.append('Sep 02 09:0%d:12 web-01 nginx[1184]: 10.0.1.4 - - "GET /api/v1/health HTTP/1.1" 200 17 "-" "kube-probe/1.29"' % (n % 10))
long_out = "\n".join(head + tail_lines)

F["exec"] = {"input":{"host":"web-01","command":"systemctl status nginx --no-pager -l","session":"deploy","timeout_s":60,"max_lines":200},
             "result": sc({"run_id":"run_01K4M7X2QF","stdout":long_out+"\n","stderr":"","output":long_out,"exit_code":0,
                           "cwd":"/etc/nginx","timeout":False,"truncated":True,"artifact_id":"art_9f2c1b7e",
                           "total_lines":1842,"total_bytes":186432,"stdout_bytes":186432,"stderr_bytes":0,"output_recorded":True})}
F["exec"]["error"] = {"content":[{"type":"text","text":"host not authorized: web-09"}],"isError":True}

F["session_env"] = {"input":{"host":"build-runner","session":"ci","set":{"NODE_ENV":"production"}},
                    "result": sc({"env":{"NODE_ENV":"production","PATH":"/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
                                         "GOFLAGS":"-mod=readonly","HTTPS_PROXY":"http://10.0.0.8:3128","LANG":"C.UTF-8",
                                         "CGO_ENABLED":"0","TZ":"Asia/Shanghai"}})}

# execx.ReadArtifact 输出的每行形如 "行号:正文"，卡片要按这个格式把行号拆到侧边栏
art = []
for n in range(401, 481):
    art.append('%d:2026-09-02T09:%02d:%02d web-01 nginx[1184]: 10.0.1.%d - - "GET /api/v1/items?page=%d HTTP/1.1" 200 %d'
               % (n, n % 60, (n * 7) % 60, n % 250, n, 300 + n))
F["output_read"] = {"input":{"artifact_id":"art_9f2c1b7e","offset":401,"limit":80},
                    "result": sc({"content":"\n".join(art),"total_lines":1842})}

F["exec_many"] = {"input":{"hosts":["web-01","web-02","db-main","cache-01","build-runner"],"command":"nginx -v 2>&1 || true","timeout_s":30},
                  "result": sc({"results":[
                    {"host":"web-01","run_id":"run_A1","exit_code":0,"timeout":False,"output":"nginx version: nginx/1.24.0 (Ubuntu)"},
                    {"host":"web-02","run_id":"run_A2","exit_code":0,"timeout":False,"output":"nginx version: nginx/1.24.0 (Ubuntu)"},
                    {"host":"db-main","run_id":"run_A3","exit_code":127,"timeout":False,"output":"bash: line 1: nginx: command not found"},
                    {"host":"cache-01","run_id":"","exit_code":-1,"timeout":False,"output":"","error":"dial tcp 10.0.2.21:22: connect: connection refused"},
                    {"host":"build-runner","run_id":"run_A5","exit_code":-1,"timeout":True,"output":"（命令在 30 秒后被中断）"}]})}

F["job_start"] = {"input":{"host":"build-runner","command":"make release VERSION=v0.2.0","cwd":"/srv/onessh"},
                  "result": sc({"job_id":"job_7cf3a91e","run_id":"run_01K4M8ZZ01","pid":48213})}

def job(idx, jid, status, cmd, exit_code=None, fin=None, logb=0):
    return {"job":{"id":jid,"host_id":5,"command":cmd,"cwd":"/srv/onessh","pid":48000+idx,"used_setsid":True,
                   "status":status,"exit_code":exit_code,"started_at":ago(3*HOUR)+idx*90,"finished_at":fin},
            "log_bytes":logb}

F["job_list"] = {"input":{}, "result": sc({"jobs":[
    job(1,"job_7cf3a91e","running","make release VERSION=v0.2.0",None,None,182934),
    job(2,"job_2b81dd40","exited","tar -czf /backup/db-$(date +%F).tar.gz /var/lib/postgresql",0,ago(2*HOUR),412),
    job(3,"job_ee09c517","exited","rsync -a --delete /srv/assets/ backup:/srv/assets/",23,ago(90*MIN),88231),
    job(4,"job_51ac7f23","lost","tcpdump -i eth0 -w /tmp/cap.pcap",None,None,0),
    job(5,"job_9d4e12ab","killed","python3 -u train.py --epochs 200",None,ago(35*MIN),1490233),
]})}
F["job_status"] = {"input":{"job_id":"job_7cf3a91e"},
                   "result": sc(job(1,"job_7cf3a91e","running","make release VERSION=v0.2.0",None,None,182934))}

pack = []
for n, target in enumerate(["linux_amd64","linux_arm64","darwin_amd64","darwin_arm64","freebsd_amd64","freebsd_arm64"]):
    pack.append("[14:%02d] packing dist/onessh_0.2.0_%s.tar.gz" % (n, target))
joblog = "\n".join([
 "go build -trimpath -o /out/onessh ./cmd/onessh",
 "==> 编译 linux/amd64",
 "==> 编译 linux/arm64",
 "==> 编译 darwin/arm64",
 "go: downloading github.com/modelcontextprotocol/go-sdk v1.7.0",
 "go: downloading golang.org/x/crypto v0.44.0",
] + pack + ["sha256sum ./*.tar.gz > checksums.txt", "make: *** [Makefile:31: verify] Error 1"])
F["job_logs"] = {"input":{"job_id":"job_7cf3a91e","tail_lines":100}, "result": sc({"output":joblog})}
F["job_kill"] = {"input":{"job_id":"job_51ac7f23","signal":"TERM"}, "result": sc({"ok":True})}

conf = "\n".join([
 "user  www-data;","worker_processes  auto;","pid /run/nginx.pid;","","events {","    worker_connections  4096;",
 "    multi_accept on;","}","","http {","    sendfile            on;","    tcp_nopush          on;",
 "    types_hash_max_size 2048;","    server_tokens       off;","","    include             /etc/nginx/mime.types;",
 "    default_type        application/octet-stream;","",
 "    ssl_protocols       TLSv1.2 TLSv1.3;","    ssl_prefer_server_ciphers off;","",
 "    access_log  /var/log/nginx/access.log;","    error_log   /var/log/nginx/error.log warn;","",
 "    gzip on;","    gzip_types text/plain application/json application/javascript text/css;","",
 "    include /etc/nginx/conf.d/*.conf;","    include /etc/nginx/sites-enabled/*;","}"])
# files.Manager.Read 返回的正文每行带 "行号:" 前缀
numbered_conf = "\n".join("%d:%s" % (i + 1, line) for i, line in enumerate(conf.split("\n")))
F["file_read"] = {"input":{"host":"web-01","path":"/etc/nginx/nginx.conf","offset":1,"limit":500},
                  "result": sc({"content":numbered_conf,"sha256":"3b1f0c9a77d4e5628f14b0a9c3e7d215884ab6f0c9d3e71a25b8f4c60d97ea31",
                                "bytes":812,"total_lines":30})}
F["file_write"] = {"input":{"host":"web-01","path":"/etc/nginx/conf.d/gzip.conf","content":"gzip_comp_level 5;\n","mode":"0644"},
                   "result": sc({"sha256":"9c1d7e4a02b58f36ad91c0e7452b8d9f31607ca4de82b195f0c73a6e8d24b7f5","bytes":19,
                                 "non_atomic":True,"warning":"目标文件系统不支持覆盖 rename，已退化为先删后写"})}
diff = "\n".join([
 "--- /etc/nginx/nginx.conf","+++ /etc/nginx/nginx.conf","@@ -17,7 +17,8 @@",
 "     ssl_protocols       TLSv1.2 TLSv1.3;","     ssl_prefer_server_ciphers off;"," ",
 "-    access_log  /var/log/nginx/access.log;","+    access_log  /var/log/nginx/access.log combined buffer=32k flush=5s;",
 "+    log_not_found off;","     error_log   /var/log/nginx/error.log warn;"," ","     gzip on;","@@ -25,4 +26,5 @@",
 "     gzip_types text/plain application/json application/javascript text/css;"," ",
 "+    client_max_body_size 32m;","     include /etc/nginx/conf.d/*.conf;"])
F["file_edit"] = {"input":{"host":"web-01","path":"/etc/nginx/nginx.conf",
                           "edits":[{"old_text":"access_log  /var/log/nginx/access.log;","new_text":"access_log  /var/log/nginx/access.log combined buffer=32k flush=5s;"}],
                           "expected_sha256":"3b1f0c9a77d4e5628f14b0a9c3e7d215884ab6f0c9d3e71a25b8f4c60d97ea31"},
                  "result": sc({"sha256":"c47a0e9b13d2f68501ba7cd394e2f8016d5b93ca27e40f8a16b3d97c25ea0f43","bytes":901,"diff":diff})}
entries = [
 {"name":"conf.d","size":4096,"mode":"drwxr-xr-x","mtime":ago(6*HOUR),"directory":True},
 {"name":"sites-available","size":4096,"mode":"drwxr-xr-x","mtime":ago(21*DAY),"directory":True},
 {"name":"sites-enabled","size":4096,"mode":"drwxr-xr-x","mtime":ago(21*DAY),"directory":True},
 {"name":"snippets","size":4096,"mode":"drwxr-xr-x","mtime":ago(140*DAY),"directory":True},
 {"name":"fastcgi.conf","size":1077,"mode":"-rw-r--r--","mtime":ago(233*DAY),"directory":False},
 {"name":"mime.types","size":5349,"mode":"-rw-r--r--","mtime":ago(233*DAY),"directory":False},
 {"name":"nginx.conf","size":901,"mode":"-rw-r--r--","mtime":ago(9*MIN),"directory":False},
 {"name":"proxy_params","size":355,"mode":"-rw-r--r--","mtime":ago(233*DAY),"directory":False},
 {"name":"ssl.conf","size":214,"mode":"lrwxrwxrwx","mtime":ago(38*DAY),"directory":False,"symlink_target":"/etc/letsencrypt/options-ssl-nginx.conf"},
 {"name":"scgi_params","size":636,"mode":"-rw-r--r--","mtime":ago(233*DAY),"directory":False},
]
F["file_list"] = {"input":{"host":"web-01","path":"/etc/nginx"}, "result": sc({"entries":entries}),
                  "calls":{"file_read": F["file_read"]["result"]}}
F["file_transfer"] = {"input":{"src_host":"web-01","src_path":"/var/log/nginx/access.log.1","dst_host":"build-runner","dst_path":"/tmp/access.log.1"},
                      "result": sc({"bytes":48213904,"source_sha256":"71f0a2c9b8d34e56a70f12c8b943de507a1c6f8b02d94e73a5c108bf42e7d961",
                                    "destination_sha256":"71f0a2c9b8d34e56a70f12c8b943de507a1c6f8b02d94e73a5c108bf42e7d961","verified":True})}

grep_lines = []
for path, base in [("/etc/nginx/nginx.conf", 20), ("/etc/nginx/sites-enabled/api.conf", 44), ("/etc/nginx/conf.d/tls.conf", 7)]:
    grep_lines.append({"path":path,"line":base-1,"text":"    server_name api.example.com;","match":False})
    grep_lines.append({"path":path,"line":base,"column":5,"text":"    ssl_protocols TLSv1.2 TLSv1.3;","match":True})
    grep_lines.append({"path":path,"line":base+1,"text":"    ssl_session_cache shared:SSL:10m;","match":False})
F["grep"] = {"input":{"host":"web-01","pattern":"ssl_protocols","path":"/etc/nginx","context":1,"limit":100},
             "result": sc({"lines":grep_lines,"match_count":3,"truncated":False,"engine":"rg"})}
F["find"] = {"input":{"host":"web-01","pattern":"**/*.conf","path":"/etc/nginx","limit":1000},
             "result": sc({"paths":["/etc/nginx/nginx.conf","/etc/nginx/conf.d/gzip.conf","/etc/nginx/conf.d/tls.conf",
                                    "/etc/nginx/conf.d/upstream.conf","/etc/nginx/sites-available/api.conf",
                                    "/etc/nginx/sites-available/default.conf","/etc/nginx/sites-enabled/api.conf",
                                    "/etc/nginx/snippets/fastcgi-php.conf","/etc/nginx/snippets/snakeoil.conf"],
                           "truncated":False,"engine":"helper",
                           "warning":"远端未安装 fd，已通过临时 helper 遍历"})}

m1 = "web-01 的 nginx 配置在 /etc/nginx，reload 用 systemctl reload nginx，不要用 restart（会断掉长连接）。"
m2 = "web-01 与 web-02 前面有 LB，改配置要两台同时改，否则会出现 50% 请求失败。"
m3 = "所有 prod 主机的 systemd 单元改动后必须 daemon-reload，否则重启会读到旧单元。"
m4 = "web-01 的证书由 certbot 自动续期，cron 在每周日 03:15，续期后会自动 reload nginx。"
m5 = "web-01 磁盘 /var 只有 20G，access.log 每天涨 1.5G，logrotate 保留 7 天。"
F["memory_remember"] = {"input":{"host":"web-01","content":m1,"importance":0.8,"veracity":"tool"},
                        "result": sc({"id":184,"bank":"web-01","deduped":False,"embedded":True})}
recalls = [
 {"id":184,"bank":"web-01","host_id":1,"content":m1,"source":"mcp","importance":0.8,"veracity":"tool",
  "score":0.91,"fts_score":0.74,"dense_score":0.88,"created_at":ago(2*MIN),"recall_count":6},
 {"id":151,"bank":"web-01","host_id":1,"content":m2,"source":"mcp","importance":0.75,"veracity":"stated",
  "score":0.77,"fts_score":0.61,"dense_score":0.72,"created_at":ago(9*DAY),"recall_count":3},
 {"id":92,"bank":"global","host_id":None,"content":m3,"source":"mcp","importance":0.6,"veracity":"inferred",
  "score":0.52,"fts_score":0.40,"dense_score":0.55,"created_at":ago(60*DAY),"recall_count":11},
]
F["memory_recall"] = {"input":{"host":"web-01","query":"nginx 配置改完怎么生效","limit":8},
                      "result": sc({"results":recalls,"engine":"hybrid"})}
F["memory_list"] = {"input":{"host":"web-01","limit":50,"offset":0}, "result": sc({"memories":[
 {"id":184,"content":m1,"source":"mcp","importance":0.8,"veracity":"tool","created_at":ago(2*MIN),"updated_at":ago(2*MIN),"recall_count":6},
 {"id":151,"content":m2,"source":"mcp","importance":0.75,"veracity":"stated","created_at":ago(9*DAY),"updated_at":ago(7*DAY),"recall_count":3},
 {"id":130,"content":m4,"source":"mcp","importance":0.6,"veracity":"tool","created_at":ago(63*DAY),"updated_at":ago(63*DAY),"recall_count":1},
 {"id":118,"content":m5,"source":"mcp","importance":0.55,"veracity":"tool","created_at":ago(33*DAY),"updated_at":ago(33*DAY),"recall_count":0},
], "bank":"web-01"})}
F["memory_update"] = {"input":{"id":184,"importance":0.9}, "result": sc({"id":184,"embedded":True})}
F["memory_forget"] = {"input":{"id":118}, "result": sc({"deleted":True})}
F["memory_stats"] = {"input":{}, "result": sc({"total":327,"banks":[
  {"bank":"global","count":58,"embedded":58,"last_written":ago(3*HOUR)},
  {"bank":"web-01","count":94,"embedded":91,"last_written":ago(11*MIN)},
  {"bank":"web-02","count":76,"embedded":70,"last_written":ago(28*HOUR)},
  {"bank":"db-main","count":81,"embedded":81,"last_written":ago(5*DAY)},
  {"bank":"build-runner","count":18,"embedded":4,"last_written":None}]})}
F["memory_sleep"] = {"input":{"host":"web-01"}, "result": sc({"deduped":7,"decayed":23,"pruned":4})}

F["host_status"] = {"input":{"host":"web-01","fresh":True},
                    "result": sc({"host_id":1,"ts":ago(40),"cpu_pct":37.4,"mem_used_kb":6094848,"mem_total_kb":8126464,
                                  "load1":1.27,"disks":[
                                    {"mount":"/","used_kb":38914048,"total_kb":51609600},
                                    {"mount":"/var","used_kb":19398656,"total_kb":20971520},
                                    {"mount":"/boot","used_kb":198656,"total_kb":1046528},
                                    {"mount":"/srv/assets","used_kb":104857600,"total_kb":524288000}]})}

PNG = "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"
F["image_view"] = {"input":{"host":"web-01","path":"/srv/reports/latency-p99.png","max_dim":1024},
                   "result": {"content":[{"type":"image","data":PNG,"mimeType":"image/png"},
                                         {"type":"text","text":"原始 1280x720，输出 320x180，2043 字节"}],
                              "structuredContent":{"original_width":1280,"original_height":720,"width":320,"height":180,
                                                   "bytes":2043,"mime_type":"image/png"},"isError":False}}

out = os.path.dirname(os.path.abspath(__file__))
TITLES = {
    "hosts_list": "主机列表",
    "hosts_manage_list": "主机配置",
    "host_create": "新增主机",
    "host_update": "更新主机",
    "host_test": "连通性测试",
    "host_reset_fingerprint": "重置指纹",
    "host_delete": "删除主机",
    "exec": "命令执行",
    "session_env": "会话环境变量",
    "output_read": "截断输出",
    "exec_many": "批量执行",
    "job_start": "后台任务",
    "job_list": "任务列表",
    "job_status": "任务状态",
    "job_logs": "任务日志",
    "job_kill": "终止任务",
    "file_read": "文件内容",
    "file_write": "文件写入",
    "file_edit": "文件编辑",
    "file_list": "目录列表",
    "file_transfer": "文件传输",
    "grep": "内容搜索",
    "find": "路径查找",
    "memory_remember": "保存记忆",
    "memory_recall": "召回记忆",
    "memory_list": "记忆列表",
    "memory_update": "更新记忆",
    "memory_forget": "删除记忆",
    "memory_stats": "记忆统计",
    "memory_sleep": "记忆维护",
    "host_status": "资源指标",
    "image_view": "远程图片"
}

for tool, data in F.items():
    data["title"] = TITLES[tool]
    data.setdefault("error", {"content":[{"type":"text","text":"host not authorized: web-09\n可用主机请先调用 hosts_list 确认。"}],"isError":True})
    with open(os.path.join(out, tool + ".json"), "w", encoding="utf-8") as fh:
        json.dump(data, fh, ensure_ascii=False, indent=2)
expected = ("hosts_list hosts_manage_list host_create host_update host_test host_reset_fingerprint host_delete "
            "exec session_env output_read exec_many job_start job_list job_status job_logs job_kill file_read "
            "file_write file_edit file_list file_transfer grep find memory_remember memory_recall memory_list "
            "memory_update memory_forget memory_stats memory_sleep host_status image_view").split()
print("written:", len(F), "missing:", sorted(set(expected) - set(F)))
