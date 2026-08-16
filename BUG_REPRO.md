# Bug Reproduction

## 包的性质

当前 test_model_fix 保存的是被测模型修复后的结果源码，不是初始含 Bug 源码。要复现原始缺陷，必须检出下面固定的 parent SHA；不要在当前修复结果源码上期待重新出现修复前失败。生成系统使用的可信验证补丁和完整验证日志仅在本地留存，不提交到结果分支。

## 问题现象

出发计划漏报了站不下的车列。D01 这条出发线的 capacity_ft 只有 600，出车指令 T410 的 max_length_ft 给的是 5000（按机车牵引口径定的），10 辆 60 ft 的车加一台 68 ft 的机车（含车钩间隙）编出来 length_ft=690，计划里这趟车 findings 一条 error 都没有，train 也被标成 complete，实际这个车列比出发线长了 90 ft，根本停不进去。同一条 D01 如果把 weight_limit_tons 调低，超吨会照常报出 “exceeds departure track D01 limit”，只有长度这一项永远不报；把出发线 capacity_ft 放大到 5000 以上结论也一样（没有 error），说明它压根没参照出发线的长度。请修复出发线长度校验，让编组长度超过出发线容量时给出 error 级 finding 并同时出现在车列与计划两级 findings 里，同时保持出车指令自身的 max_length_ft/max_tons/max_axles 在挑车时的既有拦截、出发线重量校验、机车牵引与最小车数判定不变，并保证全量测试通过。

## 含 Bug 版本

- 仓库：zhanglei10281852-gif/gogo-64
- 仓库地址：https://github.com/zhanglei10281852-gif/gogo-64.git
- parent SHA：0b7422739844c106ddcb995db37cfdaaf6db947e

## 复现步骤

```bash
git clone -- https://github.com/zhanglei10281852-gif/gogo-64.git bug-repro
cd bug-repro
git checkout --detach 0b7422739844c106ddcb995db37cfdaaf6db947e
go test ./internal/depart -run "^TestBuildReportsAConsistLongerThanItsDepartureTrack$" -count=1 -v
```

## 双架构完整错误信息

### linux/amd64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/depart -run "^TestBuildReportsAConsistLongerThanItsDepartureTrack$" -count=1 -v
=== RUN   TestBuildReportsAConsistLongerThanItsDepartureTrack
    departure_track_capacity_regression_test.go:119: expected one departure track error, got 0: []
--- FAIL: TestBuildReportsAConsistLongerThanItsDepartureTrack (0.00s)
FAIL
FAIL	HumpYard/internal/depart	0.002s
FAIL

```

stderr：

```text
warning: internal/depart/departure_track_capacity_regression_test.go has type 100755, expected 100644
warning: internal/depart/departure_track_capacity_regression_test.go has type 100755, expected 100644

```

### linux/arm64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/depart -run "^TestBuildReportsAConsistLongerThanItsDepartureTrack$" -count=1 -v
=== RUN   TestBuildReportsAConsistLongerThanItsDepartureTrack
    departure_track_capacity_regression_test.go:119: expected one departure track error, got 0: []
--- FAIL: TestBuildReportsAConsistLongerThanItsDepartureTrack (0.01s)
FAIL
FAIL	HumpYard/internal/depart	0.126s
FAIL

```

stderr：

```text
warning: internal/depart/departure_track_capacity_regression_test.go has type 100755, expected 100644
warning: internal/depart/departure_track_capacity_regression_test.go has type 100755, expected 100644

```

## 通过条件

D01 capacity_ft=600、出车指令 max_length_ft=5000 时，10 辆 60 ft 车 + 68 ft 机车编成 length_ft=690 的车列产生且仅产生 1 条命中 D01 的 error 级 finding（scope=train、subject=T410），plan.Findings 同样带 1 条；把 D01 capacity_ft 放大到 5000 后同一批车不产生任何 error；把 D01 weight_limit_tons 调低后仍然产生 1 条命中 D01 的重量 error；挑车阶段按 max_length_ft/max_tons/max_axles 扣车（held）、机车牵引不足、最小车数不足等既有判定不回归；定向测试、全量 go test ./... -count=1 与 go build ./... && go vet ./... 全部通过；校准与远端复跑均在 golang:1.22 linux/amd64 单架构完成。
