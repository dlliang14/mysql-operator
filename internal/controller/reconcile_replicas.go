package controller

import (
	"context" // 用于处理上下文，提供超时、取消等操作
	"fmt"     // 格式化I/O函数，如字符串格式化和打印
	"k8s.io/apimachinery/pkg/labels"
	"regexp"
	"sigs.k8s.io/controller-runtime/pkg/client" // 提供与 Kubernetes API 交互的客户端
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strconv"

	databasev1 "github.com/dlliang14/api/v1" // 导入自定义的 MySQLCluster API 资源定义
	v1 "k8s.io/api/core/v1"                  // 核心 Kubernetes API 对象，例如 Pod 和 Service
	ctrl "sigs.k8s.io/controller-runtime"
)

func (r *MysqlClusterReconciler) reconcileReplicas(ctx context.Context, cluster databasev1.MysqlCluster) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	actualReplicaCount, actualReplicaNames := r.getActualReplicaInfo(ctx, cluster) //获取实际副本数
	expectedReplicaCount := cluster.Spec.Replicas

	if actualReplicaCount != expectedReplicaCount {
		log.Info("副本数与预期不符", "实际副本数", actualReplicaCount, "预期副本数", expectedReplicaCount)

		// 生成实际副本编号集合
		actualPodNumbers := extractPodNumbers(actualReplicaNames)

		// 生成预期副本编号集合
		expectedPodNumbers := generateExpectedPodNumbers(int(expectedReplicaCount))

		// 计算缺失的 Pod 编号
		missingPodNumbers := findMissingPodNumbers(expectedPodNumbers, actualPodNumbers)

		// 创建缺失的 Pod 和 ConfigMap
		for _, podNumber := range missingPodNumbers {
			podName := fmt.Sprintf("mysql-%s", podNumber)
			configMapName := fmt.Sprintf("mysql-config-%s", podNumber)

			// 如果volume不存在需要创建，方法内部有判断机制
			pvcName := fmt.Sprintf("mysql-%s", podNumber) // pvc名字与pod名是一致的
			storageClassName := cluster.Spec.Storage.StorageClassName
			storageSize := cluster.Spec.Storage.Size
			if err := r.createPVC(ctx, pvcName, storageClassName, cluster.Namespace, storageSize, &cluster); err != nil {
				return ctrl.Result{}, err
			}

			// 如果是全新的副本则需要创建出对应的configmap
			serverIdNum, _ := strconv.Atoi(podNumber) // podNumber格式为如01、02、03，需要转换
			if err := r.createConfigMap(ctx, configMapName, serverIdNum, cluster.Namespace, &cluster); err != nil {
				return ctrl.Result{}, err
			}

			if err := r.createPod(ctx, podName, cluster.Spec.Image, configMapName, pvcName, cluster.Namespace, &cluster); err != nil {
				return ctrl.Result{}, err
			}

			log.Info("创建缺失的 Pod", "PodName", podName)
		}
	}

	return ctrl.Result{}, nil
}

// 获取实际的副本数
func (r *MysqlClusterReconciler) getActualReplicaInfo(ctx context.Context, cluster databasev1.MysqlCluster) (int32, []string) {
	log := log.FromContext(ctx)

	// 创建一个 Pod 列表对象
	podList := &v1.PodList{}

	// 创建 ListOptions，根据需要筛选 Pod（使用标签选择器或其他筛选条件）
	listOptions := &client.ListOptions{
		Namespace:     cluster.Namespace,
		LabelSelector: labels.SelectorFromSet(map[string]string{"app": "mysql"}),
	}

	// 获取 Pod 列表
	if err := r.List(ctx, podList, listOptions); err != nil {
		log.Error(err, "获取 Pod 列表失败")
		return 0, nil
	}

	// 提取 Pod 名称
	var podNames []string
	for _, pod := range podList.Items {
		podNames = append(podNames, pod.Name)
	}

	log.Info("当前副本情况", "副本数", len(podList.Items), "PodNames", podNames, "预期副本数", cluster.Spec.Replicas)

	// 计算实际副本数
	return int32(len(podList.Items)), podNames
}

// extractPodNumbers 提取 Pod 名称中的编号
func extractPodNumbers(podNames []string) []string {
	var podNumbers []string
	var podNamePattern = regexp.MustCompile(`mysql-(\d+)`)

	for _, name := range podNames {
		if matches := podNamePattern.FindStringSubmatch(name); len(matches) > 1 {
			podNumbers = append(podNumbers, matches[1])
		}
	}
	return podNumbers
}

// generateExpectedPodNumbers 生成预期的 Pod 编号集合
func generateExpectedPodNumbers(count int) []string {
	var podNumbers []string
	for i := 1; i <= count; i++ {
		podNumbers = append(podNumbers, fmt.Sprintf("%02d", i))
	}
	return podNumbers
}

// findMissingPodNumbers 计算缺失的 Pod 编号
func findMissingPodNumbers(expected, actual []string) []string {
	expectedSet := make(map[string]struct{}, len(expected))
	for _, pod := range expected {
		expectedSet[pod] = struct{}{}
	}

	for _, pod := range actual {
		delete(expectedSet, pod)
	}

	var missingPodNumbers []string
	for pod := range expectedSet {
		missingPodNumbers = append(missingPodNumbers, pod)
	}
	return missingPodNumbers
}
