# BENZHI_README

## 项目说明

- 项目：zhanglei10281852-gif/gogo-64
- 项目用途：HumpYard is an offline command line planner for railway hump yard (classification yard) car marshalling. It reads a yard configuration and a yard order, then works out how the arriving cars should be blocked, humped, stored in the bowl, pulled into outbound trains and covered by crews, and it records the result in a local hash-chained store.
- Go 工具链：`golang:1.22`
- 前端工具链：无

## 标准构建、运行和测试命令

进入容器后执行：

```bash
# 编译
cd '/app' && GOTOOLCHAIN=local go build ./...

# 启动
cd '/app' && GOTOOLCHAIN=local go run ./cmd/humpyard

# 测试
cd '/app' && GOTOOLCHAIN=local go test ./...
```

## Docker 构建和进入容器

```bash
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh benzhi-task-64-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-task-64-arm64 linux/arm64
docker run -it benzhi-task-64-amd64:latest
docker run -it --platform linux/arm64 benzhi-task-64-arm64:latest
```

## 题目验证命令

1. 预期退出码 0：`go test ./internal/depart -run "^TestBuildReportsAConsistLongerThanItsDepartureTrack$" -count=1 -v`
2. 预期退出码 0：`go test -buildvcs=false -count=1 ./...`

## Bug 复现

Bug 现象、触发步骤和完整错误信息见 `BUG_REPRO.md`。
