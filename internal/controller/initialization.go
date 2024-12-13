package controller

import (
	"context" // 用于处理上下文，提供超时、取消等操作
	"fmt"     // 格式化I/O函数，如字符串格式化和打印
	"sigs.k8s.io/controller-runtime/pkg/log"

	databasev1 "github.com/dlliang14/api/v1" // 导入自定义的 MySQLCluster API 资源定义
)

// 初始化：控制器刚启动时执行一次
func (r *MysqlClusterReconciler) init(ctx context.Context, cluster *databasev1.MysqlCluster) error {
	// 1、创建主库和从库的 Service
	log := log.FromContext(ctx)

	if _, err := r.getOrCreateService(ctx, cluster.Spec.MasterService, "master", cluster.Namespace, *cluster); err != nil {
		log.Info("创建svc失败1")
		return fmt.Errorf("failed to create master service: %v", err)
	}
	if _, err := r.getOrCreateService(ctx, cluster.Spec.SlaveService, "slave", cluster.Namespace, *cluster); err != nil {
		log.Info("创建svc失败1")

		return fmt.Errorf("failed to create slave service: %v", err)
	}

	// 2、读取读取副本数
	replicas := cluster.Spec.Replicas
	if replicas < 1 {
		return fmt.Errorf("invalid replica count: %d", replicas)
	}

	// 3、根据副本数创建出cm
	for i := int32(1); i <= replicas; i++ {
		configMapName := fmt.Sprintf("mysql-config-%02d", i) // 名称格式为mysql-config-01，注%02d代表有两位数字，不足位则补0
		serverID := int(i)                                   // 这里的 serverID 与副本序号一一对应

		if err := r.createConfigMap(ctx, configMapName, serverID, cluster.Namespace, cluster); err != nil {
			return fmt.Errorf("failed to create configmap %s: %v", configMapName, err)
		}
	}

	// 4、根据副本数创建出pvc
	storageClassName := cluster.Spec.Storage.StorageClassName
	storageSize := cluster.Spec.Storage.Size
	for i := int32(1); i <= replicas; i++ {
		pvcName := fmt.Sprintf("mysql-%02d", i)
		if err := r.createPVC(ctx, pvcName, storageClassName, cluster.Namespace, storageSize, cluster); err != nil {
			return err
		}
	}

	// 5、根据副本数创建出Pod
	for i := int32(1); i <= replicas; i++ {
		podName := fmt.Sprintf("mysql-%02d", i)
		pvcName := fmt.Sprintf("mysql-%02d", i)
		log.Info("准备创pod", "podName", podName)
		configMapName := fmt.Sprintf("mysql-config-%02d", i) // 名称格式为mysql-01
		//if err := r.createPod(ctx, podName, cluster.Spec.Image, cluster.Spec.MasterConfig, cluster.Namespace, cluster); err != nil {
		if err := r.createPod(ctx, podName, cluster.Spec.Image, configMapName, pvcName, cluster.Namespace, cluster); err != nil {
			return fmt.Errorf("failed to create pod %s: %v", podName, err)
		}
	}

	// 6、制作主从关系
	masterPodName := "mysql-01"
	slavePodNames := []string{}
	for i := int32(2); i <= replicas; i++ {
		slavePodNames = append(slavePodNames, fmt.Sprintf("mysql-%02d", i))
	}
	log.Info("init函数", "masterPodName", masterPodName, "slavePodNames", slavePodNames)

	if err := r.setupMasterSlaveReplication(ctx, masterPodName, slavePodNames, *cluster); err != nil {
		log.Info("制作主从失败", "err", err)

		return fmt.Errorf("failed to setup master-slave: %v", err)
	}

	// 7、启动 GTID 快照更新协程
	// r.startAndUpdateGTIDSnapshot(ctx)

	return nil
}
