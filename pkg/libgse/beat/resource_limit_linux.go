// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beat

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/containerd/cgroups"
	"github.com/opencontainers/runtime-spec/specs-go"
)

// SetResourceLimit 设置进程 CPU 和内存资源限制
// Name: cgroup 名称; CPU: core; MEM: MB
func SetResourceLimit(name string, cpu float64, mem int) {
	if err := setLinuxCgroups(name, cpu, mem); err != nil {
		// CPU 核数向上取整 确保有核可用
		// 0.1 -> 1 core
		runtime.GOMAXPROCS(int(math.Ceil(cpu)))
		return
	}

	// 如果 cgroup 限制设置成功 则允许进程在所有核心上进行调度
	runtime.GOMAXPROCS(0)
}

func setLinuxCgroups(name string, cpu float64, mem int) error {
	mode := cgroups.Mode()
	switch mode {
	case cgroups.Legacy, cgroups.Hybrid:
		// v1 和 混合模式下选择 cgroup v1
		return setLinuxCgroupsV1(name, cpu, mem)
	case cgroups.Unified:
		// 仅支持 v2 模式下才选择 cgroup v2
		return setLinuxCgroupsV2(name, cpu, mem)
	default:
		return errors.New("no support cgroup mode")
	}
}

// setLinuxCgroupsV2 通过直接操作 cgroup v2 伪文件系统设置资源限制
// 路径: /sys/fs/cgroup/collector-<name>/
//   cpu.max    -> "quota period" (例如 "100000 100000")
//   memory.max -> limit in bytes
//   cgroup.procs -> PID
func setLinuxCgroupsV2(name string, cpu float64, mem int) error {
	cgroupPath := filepath.Join("/sys/fs/cgroup", "collector-"+name)

	// 创建 cgroup 目录
	if err := os.MkdirAll(cgroupPath, 0755); err != nil {
		return fmt.Errorf("create cgroup dir %s: %w", cgroupPath, err)
	}

	hasLimit := false

	// 设置 CPU 限制（格式: "quota period"）
	const cpuPeriod = 100000
	cpuQuota := int64(cpu * cpuPeriod)
	if cpuQuota > 0 {
		cpuMax := fmt.Sprintf("%d %d", cpuQuota, cpuPeriod)
		if err := os.WriteFile(
			filepath.Join(cgroupPath, "cpu.max"),
			[]byte(cpuMax), 0644,
		); err != nil {
			return fmt.Errorf("write cpu.max: %w", err)
		}
		hasLimit = true
	} else if cpuQuota < 0 {
		// 负数表示无限制
		if err := os.WriteFile(
			filepath.Join(cgroupPath, "cpu.max"),
			[]byte("max"), 0644,
		); err != nil {
			return fmt.Errorf("write cpu.max: %w", err)
		}
		hasLimit = true
	}

	// 设置内存限制（单位: bytes）
	memLimit := int64(mem) * 1024 * 1024
	if memLimit > 0 {
		if err := os.WriteFile(
			filepath.Join(cgroupPath, "memory.max"),
			[]byte(strconv.FormatInt(memLimit, 10)), 0644,
		); err != nil {
			return fmt.Errorf("write memory.max: %w", err)
		}
		hasLimit = true
	}

	if !hasLimit {
		return nil
	}

	// 将当前进程加入 cgroup
	pidStr := strconv.FormatInt(int64(os.Getpid()), 10)
	if err := os.WriteFile(
		filepath.Join(cgroupPath, "cgroup.procs"),
		[]byte(pidStr), 0644,
	); err != nil {
		return fmt.Errorf("write cgroup.procs: %w", err)
	}

	return nil
}

func setLinuxCgroupsV1(name string, cpu float64, mem int) error {
	resource := &specs.LinuxResources{}
	var unlimited int64 = -1

	// cpu * Core: 小于等于 0 表示 cgroup 无 CPU 限制
	cpuQuota := int64(cpu * 100000)
	if cpuQuota > 0 {
		resource.CPU = &specs.LinuxCPU{Quota: &cpuQuota}
	}
	if cpuQuota < 0 {
		resource.CPU = &specs.LinuxCPU{Quota: &unlimited}
	}

	// mem * MB: 小于等于 0 表示 cgroup 无内存限制
	memLimit := int64(mem) * 1024 * 1024
	if memLimit > 0 {
		resource.Memory = &specs.LinuxMemory{Limit: &memLimit}
	}
	if memLimit < 0 {
		resource.Memory = &specs.LinuxMemory{Limit: &unlimited}
	}

	// 无任何限制 直接返回
	if resource.CPU == nil && resource.Memory == nil {
		return nil
	}

	// 静态路径
	staticPath := cgroups.StaticPath("/collector-" + name)

	// 先尝试加载原有的 cgroup
	cgroup, err := cgroups.Load(cgroups.V1, staticPath)
	// 加载成功
	if err == nil {
		// 先尝试更新 cgroup 配置 可能每次启动的时候限制资源数量会不同
		if err = cgroup.Update(resource); err != nil {
			return err
		}
		// 将进程号挂到 cgroup 下
		return cgroup.Add(cgroups.Process{Pid: os.Getpid()})
	}

	// 如果有问题 则尝试创建一个新的 cgroup 再挂载 实在还是不行那就木得办法了
	cgroup, err = cgroups.New(cgroups.V1, staticPath, resource)
	if err != nil {
		return err
	}

	// 创建成功 将进程号挂到 cgroup 下
	return cgroup.Add(cgroups.Process{Pid: os.Getpid()})
}
