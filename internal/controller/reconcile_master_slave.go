package controller

import (
	"context"                                   // 用于处理上下文，提供超时、取消等操作
	"fmt"                                       // 格式化I/O函数，如字符串格式化和打印
	"sigs.k8s.io/controller-runtime/pkg/client" // 提供与 Kubernetes API 交互的客户端
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strings"

	databasev1 "github.com/dlliang14/api/v1" // 导入自定义的 MySQLCluster API 资源定义
	v1 "k8s.io/api/core/v1"                // 核心 Kubernetes API 对象，例如 Pod 和 Service
	ctrl "sigs.k8s.io/controller-runtime"
)

// 主从调谐逻辑
func (r *MysqlClusterReconciler) reconcileMasterSlave(ctx context.Context, cluster databasev1.MysqlCluster) (ctrl.Result, error) {
	// 检查主库状态
	masterAlive, err := r.checkMasterStatus(ctx, cluster)
	if err != nil {
		// 如果检查主库状态时出现错误，则返回错误并重新排队调谐
		return ctrl.Result{}, err
	}

	if !masterAlive {
		// 主库挂掉的情况处理
		if err := r.handleMasterFailure(ctx, cluster); err != nil {
			// 如果处理主库故障时出现错误，则返回错误并重新排队调谐
			return ctrl.Result{}, err
		}
	} else {
		// 主库正常时，检查所有从库的状态
		masterPodName, failedReplicas, err := r.checkReplicaStatus(ctx, cluster)
		if err != nil {
			// 如果检查从库状态时出现错误，则返回错误并重新排队调谐
			return ctrl.Result{}, err
		}

		// 重新配置失败的从库
		if err := r.setupMasterSlaveReplication(ctx, masterPodName, failedReplicas, cluster); err != nil {
			// 如果重新配置失败，则返回错误并重新排队调谐
			return ctrl.Result{}, err
		}

		// 确保所有非主库的 Pod 标签都是 slave
		if err := r.ensureSlaveRoles(ctx, cluster); err != nil {
			// 如果确保从库标签时出现错误，则返回错误并重新排队调谐
			return ctrl.Result{}, err
		}
	}

	// 所有操作成功后，返回成功结果
	return ctrl.Result{}, nil
}

// 通过查看 master-service 关联的 endpoint 是否为空来判断主库是否挂掉：
func (r *MysqlClusterReconciler) checkMasterStatus(ctx context.Context, cluster databasev1.MysqlCluster) (bool, error) {
	// 获取 master-service 关联的 Endpoints
	endpoints := &v1.Endpoints{}
	if err := r.Get(ctx, client.ObjectKey{Name: cluster.Spec.MasterService, Namespace: cluster.Namespace}, endpoints); err != nil {
		return false, err
	}

	// 检查 endpoints 中是否有 addresses，若为空则表示主库挂掉
	if len(endpoints.Subsets) == 0 || len(endpoints.Subsets[0].Addresses) == 0 {
		return false, nil
	}

	return true, nil
}

// 当主库挂掉时，需要选举新的主库并重新配置主从关系：
func (r *MysqlClusterReconciler) handleMasterFailure(ctx context.Context, cluster databasev1.MysqlCluster) error {
	log := log.FromContext(ctx)

	// 选举新的主库（假设选举逻辑已经实现）
	newMasterName, remainingSlaves, err := r.electNewMaster(ctx, cluster)
	if err != nil {
		return err
	}

	log.Info("选举出新主库", "newMasterName", newMasterName, "remainingSlaves", remainingSlaves)

	// 重新配置主从关系
	err = r.setupMasterSlaveReplication(ctx, newMasterName, remainingSlaves, cluster)
	if err != nil {
		return err
	}

	return nil
}

// 检查所有从pod的主从状态，返回当前主库名与所以异常的从pod名构成的数组
func (r *MysqlClusterReconciler) checkReplicaStatus(ctx context.Context, cluster databasev1.MysqlCluster) (string, []string, error) {
	/*
		1、调用已有的函数getMasterPodNameFromEndpoints、getReplicaPodsNames来获取所有的从pod名字
		2、判断所有从pod名字的主从状态，判断依据是判断sql线程与io线程同时为yes代表成功否则失败，执行命令是调用已有函数func (r *MysqlClusterReconciler) execCommandOnPod(pod *v1.Pod, command string) (string, error)

		3、最后返回需要当前主库名，所有主从状态异常的从库数组
	*/
	log := log.FromContext(ctx)

	// 获取主库 Pod 名称
	masterPodName, err := r.getMasterPodNameFromEndpoints(ctx, cluster)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get master pod name: %v", err)
	}

	// 获取所有从库 Pod 名称
	replicaPodNames, err := r.getReplicaPodsNames(ctx, cluster, masterPodName)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get replica pods: %v", err)
	}

	// 准备 SQL 查询命令
	sqlQuery := fmt.Sprintf(
		"mysql -uroot -p%s -e \"SHOW SLAVE STATUS \\G\"",
		MySQLPassword,
	)

	var failedReplicas []string
	for _, replicaPodName := range replicaPodNames {
		// 获取 Pod 对象
		pod := &v1.Pod{}
		if err := r.Get(ctx, client.ObjectKey{Name: replicaPodName, Namespace: cluster.Namespace}, pod); err != nil {
			return "", nil, fmt.Errorf("failed to get pod %s: %v", replicaPodName, err)
		}

		// 执行 SQL 查询
		output, err := r.execCommandOnPod(pod, sqlQuery)
		if err != nil {
			return "", nil, fmt.Errorf("failed to execute command on pod %s: %v", replicaPodName, err)
		}

		// 解析 SQL 查询结果
		sqlThread := strings.Contains(output, "Slave_SQL_Running: Yes")
		ioThread := strings.Contains(output, "Slave_IO_Running: Yes")

		if !(sqlThread && ioThread) {
			failedReplicas = append(failedReplicas, replicaPodName)
		}
	}

	log.Info("主从状态检查完成", "主库", masterPodName, "状态失败的从库", failedReplicas)

	// 返回主库名称和所有主从状态异常的从库名称
	return masterPodName, failedReplicas, nil
}

// 该函数负责确保所有非主库的 Pod 标签都是 slave。
func (r *MysqlClusterReconciler) ensureSlaveRoles(ctx context.Context, cluster databasev1.MysqlCluster) error {
	// 获取 master-service 关联的 Pod 名称
	//endpoints := &v1.Endpoints{}
	//if err := r.Get(ctx, client.ObjectKey{Name: cluster.Spec.MasterService, Namespace: cluster.Namespace}, endpoints); err != nil {
	//	return err
	//}
	//masterPodName := endpoints.Subsets[0].Addresses[0].TargetRef.Name

	masterPodName, err := r.getMasterPodNameFromEndpoints(ctx, cluster)
	if err != nil {
		return err
	}

	// 获取所有与集群相关的 Pods
	podList := &v1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(cluster.Namespace),
		//client.MatchingLabels{"app": "mysql", "cluster": cluster.Name},
		client.MatchingLabels{"app": "mysql"},
	}
	if err := r.List(ctx, podList, listOpts...); err != nil {
		return err
	}

	// 遍历所有 Pods，确保非 master 的 Pod 的 role 都是 slave
	for _, pod := range podList.Items {
		if pod.Name != masterPodName && pod.Labels["role"] != "slave" {
			pod.Labels["role"] = "slave"
			if err := r.Update(ctx, &pod); err != nil {
				return err
			}
		}
	}

	return nil
}
