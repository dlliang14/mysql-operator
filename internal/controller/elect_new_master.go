package controller

import (
	"context" // 用于处理上下文，提供超时、取消等操作
	"fmt"     // 格式化I/O函数，如字符串格式化和打印
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client" // 提供与 Kubernetes API 交互的客户端

	databasev1 "github.com/dlliang14/api/v1" // 导入自定义的 MySQLCluster API 资源定义
	v1 "k8s.io/api/core/v1"                  // 核心 Kubernetes API 对象，例如 Pod 和 Service
)

// 选主逻辑：
/*
MHA数据一致性和选择
GTID模式：MHA会比较每个从库的GTID集合。选择包含主库GTID率高的的从库作为新的主库。
*/

func (r *MysqlClusterReconciler) electNewMaster(ctx context.Context, cluster databasev1.MysqlCluster) (string, []string, error) {
	// 定义列出从库 Pod 的选项
	listOpts := client.ListOptions{
		Namespace:     cluster.Namespace,
		LabelSelector: labels.SelectorFromSet(map[string]string{"role": "slave"}),
	}

	// 列出从库 Pods
	slavePods := &v1.PodList{}
	if err := r.List(ctx, slavePods, &listOpts); err != nil {
		return "", nil, fmt.Errorf("failed to list slave pods: %v", err)
	}

	// 初始化选举变量
	var bestSlave *v1.Pod
	var highestScore float64 = -1

	// 遍历所有从库 Pod
	for _, pod := range slavePods.Items {
		// 确保 Pod 健康
		if !isPodHealthy(pod) {
			continue
		}

		// 计算数据得分
		dataScore, err := r.getDataScore(ctx, &pod)
		if err != nil {
			return "", nil, fmt.Errorf("failed to get data score for pod %s: %v", pod.Name, err)
		}

		// 如果当前 Pod 的数据得分更高，则更新最佳 Pod
		if dataScore > highestScore {
			highestScore = dataScore
			bestSlave = &pod
		}
	}

	// 如果没有找到合适的从库，则返回错误
	if bestSlave == nil {
		return "", nil, fmt.Errorf("no suitable slave found to be promoted to master")
	}

	// 确定新的主库名称
	newMasterName := bestSlave.Name
	var remainingSlaves []string

	// 过滤掉新的主库，获取剩余的从库
	for _, pod := range slavePods.Items {
		if pod.Name != newMasterName {
			remainingSlaves = append(remainingSlaves, pod.Name)
		}
	}

	return newMasterName, remainingSlaves, nil
}

// 计算数据得分的函数
func (r *MysqlClusterReconciler) getDataScore(ctx context.Context, pod *v1.Pod) (float64, error) {
	// 获取主库的 GTID 集合快照
	masterGTIDSet := r.MasterGTIDSnapshot

	// 获取从库的 GTID 集合
	slaveGTIDSet, err := r.getSlaveGTIDSet(pod)
	if err != nil {
		return 0.0, fmt.Errorf("failed to get slave GTID set: %v", err)
	}

	// 计算 GTID 完整度得分
	gtidScore := r.calculateGTIDScore(masterGTIDSet, slaveGTIDSet)

	// 获取从库的数据量
	dataSize, err := r.getDataSize(ctx, pod)
	if err != nil {
		return 0.0, fmt.Errorf("failed to get data size: %v", err)
	}

	// 计算数据量得分
	dataScore := r.calculateDataScore(dataSize)

	// 合成最终得分
	finalScore := gtidScore + dataScore

	return finalScore, nil
}

// 获取主库的 GTID 集合
func (r *MysqlClusterReconciler) getMasterGTIDSet(pod *v1.Pod) (string, error) {
	masterCommand := "mysql -uroot -p%s -e \"SHOW MASTER STATUS\\G\" | grep 'Executed_Gtid_Set:' | awk '{print $2}'"
	command := fmt.Sprintf(masterCommand, MySQLPassword)
	gitSet, err := r.execCommandOnPod(pod, command)
	if err != nil {
		return "", err
	}
	return gitSet, nil
}

// 获取从库的 GTID 集合
func (r *MysqlClusterReconciler) getSlaveGTIDSet(pod *v1.Pod) (string, error) {
	slaveCommand := "mysql -uroot -p%s -e \"SHOW SLAVE STATUS\\G\" | grep 'Retrieved_Gtid_Set:' | awk '{print $2}'"
	command := fmt.Sprintf(slaveCommand, MySQLPassword)
	gitSet, err := r.execCommandOnPod(pod, command)
	if err != nil {
		return "", err
	}
	return gitSet, nil
}

// 计算 GTID 完整度得分
func (r *MysqlClusterReconciler) calculateGTIDScore(masterGTIDSet, slaveGTIDSet string) float64 {
	// 计算 GTID 完整度得分的逻辑
	// 例如，可以基于主库和从库的 GTID 集合的差异计算得分
	if masterGTIDSet == "" || slaveGTIDSet == "" {
		return 0.0
	}

	// 这里假设得分与从库的 GTID 集合包含主库的 GTID 集合的比例有关
	masterGTIDs := strings.Split(masterGTIDSet, ",")
	slaveGTIDs := strings.Split(slaveGTIDSet, ",")

	// 简单示例：计算从库包含的 GTID 数量
	count := 0
	for _, masterGTID := range masterGTIDs {
		for _, slaveGTID := range slaveGTIDs {
			if masterGTID == slaveGTID {
				count++
				break
			}
		}
	}

	// 得分：包含的 GTID 数量占主库 GTID 数量的比例
	return float64(count) / float64(len(masterGTIDs))
}

// 获取从库的数据量
func (r *MysqlClusterReconciler) getDataSize(ctx context.Context, pod *v1.Pod) (int64, error) {
	// 获取 MySQL 数据目录的路径
	// 这里使用了一个假设的默认路径，我们采用的容器中固定目录就是这个
	dataDirPath := "/var/lib/mysql"

	// 使用 du 命令计算数据目录的大小
	dataSizeCommand := fmt.Sprintf("du -sb %s | awk '{print $1}'", dataDirPath)
	output, err := r.execCommandOnPod(pod, dataSizeCommand)
	if err != nil {
		return 0, err
	}

	// 解析数据大小
	dataSize, err := strconv.ParseInt(strings.TrimSpace(output), 10, 64)
	if err != nil {
		return 0, err
	}

	return dataSize, nil
}

// 计算数据量得分
func (r *MysqlClusterReconciler) calculateDataScore(dataSize int64) float64 {
	// 计算数据量得分的逻辑
	// 这里简单地将数据大小作为得分值
	return float64(dataSize)
}

// 启动协程（只会启动一次）：定期更新主库的 GTID 快照
// 启动并更新 GTID 快照
func (r *MysqlClusterReconciler) startAndUpdateGTIDSnapshot(ctx context.Context, cluster databasev1.MysqlCluster) {
	go func() {
		for {
			// 标签选择器，用于筛选主库 Pod
			//labelSelector := client.MatchingLabels{"role": "master"}

			listOptions := &client.ListOptions{
				Namespace:     cluster.Namespace,
				LabelSelector: labels.SelectorFromSet(map[string]string{"role": "master"}),
			}

			podList := &v1.PodList{}

			// 获取带有标签 role: master 的 Pod
			err := r.List(ctx, podList, listOptions)
			if err != nil {
				fmt.Printf("Error listing pods: %v\n", err)
				time.Sleep(1 * time.Second) // 短暂的等待再重试
				continue
			}

			// 如果没有找到主库 Pod，清理快照并继续
			if len(podList.Items) == 0 {
				r.MasterGTIDSnapshot = ""
				time.Sleep(1 * time.Minute) // 每隔 1 分钟检查一次
				continue
			}

			// 假设只有一个主库 Pod
			pod := &podList.Items[0]

			// 更新主库的 GTID 快照
			gtidSet, err := r.getMasterGTIDSet(pod)
			if err != nil {
				fmt.Printf("Error getting master GTID set: %v\n", err)
			} else {
				r.MasterGTIDSnapshot = gtidSet
				fmt.Printf("MasterGTIDSnapshot更新成功: %v\n", gtidSet)
			}

			// 每隔 1 分钟更新一次
			time.Sleep(1 * time.Minute)
		}
	}()
}
