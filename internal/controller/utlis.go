package controller

import (
	"context" // 用于处理上下文，提供超时、取消等操作
	"fmt"     // 格式化I/O函数，如字符串格式化和打印
	"os"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors" // 提供与 Kubernetes API 相关的错误处理函数
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client" // 提供与 Kubernetes API 交互的客户端
	"sigs.k8s.io/controller-runtime/pkg/log"
	// "k8s.io/client-go/rest"

	databasev1 "github.com/dlliang14/api/v1" // 导入自定义的 MySQLCluster API 资源定义
	v1 "k8s.io/api/core/v1"                  // 核心 Kubernetes API 对象，例如 Pod 和 Service
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// 创建configmap：
func (r *MysqlClusterReconciler) createConfigMap(ctx context.Context, name string, serverID int, namespace string, cluster *databasev1.MysqlCluster) error {
	// 检查 ConfigMap 是否已经存在
	existingConfigMap := &v1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, existingConfigMap)
	if err == nil {
		// ConfigMap 已经存在
		r.Log.Info("ConfigMap already exists", "ConfigMap.Name", name)
		return nil
	}

	// 定义 OwnerReference
	ownerRef := metav1.OwnerReference{
		APIVersion: MysqlClusterAPIVersion, // 使用常量
		Kind:       MysqlClusterKind,       // 使用常量
		Name:       cluster.Name,
		UID:        cluster.UID,
		Controller: func(b bool) *bool { return &b }(true), // 使用匿名函数创建布尔值指针, 指示此 OwnerReference 是控制器，这一条非常关键
	}

	// 定义 ConfigMap 数据
	configMapData := fmt.Sprintf(`[mysqld]
server-id=%d
binlog_format=row
log-bin=mysql-bin
skip-name-resolve
gtid-mode=on
enforce-gtid-consistency=true
log-slave-updates=1
relay_log_purge=0
# other configurations`, serverID)

	// 创建 ConfigMap 定义
	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{
				ownerRef,
			},
		},
		Data: map[string]string{
			"my.cnf": configMapData,
		},
	}

	// 创建 ConfigMap
	if err := r.Create(ctx, configMap); err != nil {
		r.Log.Error(err, "Failed to create ConfigMap", "ConfigMap.Name", name)
		return err
	}
	r.Log.Info("ConfigMap created successfully", "ConfigMap.Name", name)
	return nil
}

// 创建pvc
func (r *MysqlClusterReconciler) createPVC(ctx context.Context, name, storageClassName, namespace string, storageSize string, cluster *databasev1.MysqlCluster) error {
	// 检查 PVC 是否已经存在
	existingPVC := &v1.PersistentVolumeClaim{}
	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, existingPVC)
	if err == nil {
		// PVC 已经存在
		r.Log.Info("PVC already exists", "PVC.Name", name)
		return nil
	}

	// 定义 OwnerReference
	ownerRef := metav1.OwnerReference{
		APIVersion: "database.kubebuilder.io/v1", // 根据实际的 API Version 设置
		Kind:       "MysqlCluster",               // 根据实际的 Kind 设置
		Name:       cluster.Name,
		UID:        cluster.UID,
		Controller: func(b bool) *bool { return &b }(true),
	}

	// 创建 PVC 定义
	pvc := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": "mysql",
			},
			OwnerReferences: []metav1.OwnerReference{
				ownerRef,
			},
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{
				v1.ReadWriteOnce,
			},
			Resources: v1.VolumeResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceStorage: resource.MustParse(storageSize),
				},
			},
			StorageClassName: &storageClassName,
		},
	}

	// 创建 PVC
	if err := r.Create(ctx, pvc); err != nil {
		r.Log.Error(err, "Failed to create PVC", "PVC.Name", name)
		return err
	}
	r.Log.Info("PVC created successfully", "PVC.Name", name)
	return nil
}

// 创建Pod的函数
func (r *MysqlClusterReconciler) createPod(ctx context.Context, name, image, configMapName, pvcName, namespace string, cluster *databasev1.MysqlCluster) error {
	// 检查 Pod 是否已经存在
	existingPod := &v1.Pod{}
	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, existingPod)
	if err == nil {
		// Pod 已经存在
		r.Log.Info("Pod already exists", "Pod.Name", name)
		return nil
	}

	// 定义 OwnerReference
	ownerRef := metav1.OwnerReference{
		APIVersion: MysqlClusterAPIVersion, // 使用常量
		Kind:       MysqlClusterKind,       // 使用常量
		Name:       cluster.Name,
		UID:        cluster.UID,
		Controller: func(b bool) *bool { return &b }(true), // 使用匿名函数创建布尔值指针, 指示此 OwnerReference 是控制器，这一条非常关键
	}

	// 获取resources资源限制
	resources := v1.ResourceRequirements{
		Requests: v1.ResourceList{
			v1.ResourceCPU:    resource.MustParse(cluster.Spec.Resources.Requests.CPU),
			v1.ResourceMemory: resource.MustParse(cluster.Spec.Resources.Requests.Memory),
		},
		Limits: v1.ResourceList{
			v1.ResourceCPU:    resource.MustParse(cluster.Spec.Resources.Limits.CPU),
			v1.ResourceMemory: resource.MustParse(cluster.Spec.Resources.Limits.Memory),
		},
	}

	// 创建 Pod 定义
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": "mysql",
				//"role": role,  // 后续制作完主从后会打上该标签
			},

			// 当一个对象（例如自定义资源）被删除时，Kubernetes 会自动删除与它关联的子资源。这种行为被称为级联删除。
			// 通过在子资源（例如 Pod 或 Service）中设置 OwnerReferences，Kubernetes 知道该资源是由哪个父对象创建的，并且当父对象删除时，子资源也会被自动删除。
			OwnerReferences: []metav1.OwnerReference{
				ownerRef,
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:      "mysql",
					Image:     image,
					Resources: resources, // 使用资源限制
					Env: []v1.EnvVar{
						{
							Name:  "MYSQL_ROOT_PASSWORD",
							Value: "password", // 设置 MySQL root 用户的密码
						},
					},
					Ports: []v1.ContainerPort{
						{
							Name:          "mysql",
							ContainerPort: 3306,
						},
					},
					VolumeMounts: []v1.VolumeMount{
						{
							Name:      "mysql-config",
							MountPath: "/etc/my.cnf",
							SubPath:   "my.cnf", // 使用SubPath挂载文件
						},
						{
							Name:      "mysql-data",
							MountPath: "/var/lib/mysql", // MySQL 数据目录
						},
					},
					ReadinessProbe: &v1.Probe{
						ProbeHandler: v1.ProbeHandler{
							TCPSocket: &v1.TCPSocketAction{
								Port: intstr.IntOrString{
									Type:   intstr.Int,
									IntVal: 3306,
								},
							},
						},
					},
				},
			},
			Volumes: []v1.Volume{
				{
					Name: "mysql-config",
					VolumeSource: v1.VolumeSource{
						ConfigMap: &v1.ConfigMapVolumeSource{
							LocalObjectReference: v1.LocalObjectReference{
								Name: configMapName,
							},
						},
					},
				},
				{
					Name: "mysql-data",
					VolumeSource: v1.VolumeSource{
						PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName, // PVC 名称作为参数传入
						},
					},
				},
			},
		},
	}

	// 创建 Pod
	if err := r.Create(ctx, pod); err != nil {
		r.Log.Error(err, "Failed to create Pod", "Pod.Name", name)
		return err
	}
	r.Log.Info("Pod created successfully", "Pod.Name", name)

	// 新增：等待pod就绪的功能，否则会提前制作主从，会因为pod尚未就绪而导致大量失败
	podKey := client.ObjectKey{Namespace: namespace, Name: name}
	for {
		// 等待一段时间再检查 Pod 状态
		time.Sleep(5 * time.Second)

		// 获取 Pod 状态
		pod := &v1.Pod{}
		if err := r.Get(ctx, podKey, pod); err != nil { // 在调用 r.Get 后，pod 变量将包含 Kubernetes 集群中该 Pod 的当前状态。
			r.Log.Error(err, "Failed to get Pod", "Pod.Name", name)
			//return err
			continue
		}

		// 检查 Pod 是否健康
		if isPodHealthy(*pod) {
			r.Log.Info("Pod is healthy", "Pod.Name", name)
			break
		} else {
			r.Log.Info("Waiting for Pod to become healthy", "Pod.Name", name)
		}
	}
	return nil
}

// 检查 Pod 健康的函数
func isPodHealthy(pod v1.Pod) bool {
	// 实现健康检查逻辑，例如通过 Pod 的状态、容器状态等
	return pod.Status.Phase == v1.PodRunning && len(pod.Status.ContainerStatuses) > 0 && pod.Status.ContainerStatuses[0].Ready
}

// 获取或创建 Service 的函数
func (r *MysqlClusterReconciler) getOrCreateService(ctx context.Context, serviceName, role, namespace string, cluster databasev1.MysqlCluster) (*v1.Service, error) {
	// 定义 Service 对象
	service := &v1.Service{}
	serviceKey := client.ObjectKey{Namespace: namespace, Name: serviceName}

	// 尝试获取已经存在的 Service 对象
	if err := r.Get(ctx, serviceKey, service); err != nil {
		if errors.IsNotFound(err) {
			// 如果 Service 不存在，则创建它
			service = r.createService(serviceName, role, namespace, cluster)

			// 调用 r.Create 将上面生成的 Service 对象提交给 k8s
			if err := r.Create(ctx, service); err != nil {
				return nil, err
			}
		} else {
			// 其他错误返回
			return nil, err
		}
	}

	// 返回获取到的或者新创建的 Service 对象
	return service, nil
}

// 创建 Service 对象的辅助函数
func (r *MysqlClusterReconciler) createService(name, role, namespace string, cluster databasev1.MysqlCluster) *v1.Service {
	// 定义 OwnerReference
	ownerRef := metav1.OwnerReference{
		APIVersion: MysqlClusterAPIVersion,
		Kind:       MysqlClusterKind, // MysqlCluster
		Name:       cluster.Name,     // mysqlcluster-sample
		UID:        cluster.UID,
		Controller: func(b bool) *bool { return &b }(true),
	}

	// 定义 Service 对象
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app":  "mysql",
				"role": role,
			},
			OwnerReferences: []metav1.OwnerReference{
				ownerRef,
			},
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{
				"app":  "mysql",
				"role": role,
			},
			Ports: []v1.ServicePort{
				{
					Port:       3306,
					TargetPort: intstr.FromInt(3306),
					Protocol:   v1.ProtocolTCP,
				},
			},
			Type: v1.ServiceTypeClusterIP,
		},
	}
}

// 制作主从同步的函数
func (r *MysqlClusterReconciler) setupMasterSlaveReplication(ctx context.Context, masterName string, slaveNames []string, cluster databasev1.MysqlCluster) error {
	log := log.FromContext(ctx)
	log.Info("setupMasterSlaveReplication函数", "masterName", masterName, "slaveNames", slaveNames)

	// 获取主库 Pod 对象
	masterPod := &v1.Pod{}
	masterPodKey := client.ObjectKey{Namespace: cluster.Namespace, Name: masterName}
	if err := r.Get(ctx, masterPodKey, masterPod); err != nil {
		return fmt.Errorf("failed to get master pod %s: %v", masterName, err)
	}

	// 打标签主库: 确保理解关联到主svc上，从库会通过主库的svc来连接进行同步
	if err := r.labelPod(ctx, masterName, "master", cluster); err != nil {
		return fmt.Errorf("failed to label master pod %s: %v", masterName, err)
	}

	// 为主库创建复制用户，并停止slave线程（如果之前自己是从库，那就应该停掉）
	masterCommand := fmt.Sprintf(
		"mysql -uroot -p%s -e \"CREATE USER IF NOT EXISTS 'replica'@'%%' IDENTIFIED BY 'password'; GRANT REPLICATION SLAVE ON *.* TO 'replica'@'%%';STOP slave;\"",
		MySQLPassword,
	)
	if _, err := r.execCommandOnPod(masterPod, masterCommand); err != nil {
		return fmt.Errorf("failed to execute command on master pod %s: %v", masterName, err)
	}

	// 配置每个从库: 如果从库名数组为空，则
	for _, slaveName := range slaveNames { // 如果没有从库，则循环结束，不会配置从库
		slavePod := &v1.Pod{}
		slavePodKey := client.ObjectKey{Namespace: cluster.Namespace, Name: slaveName}
		if err := r.Get(ctx, slavePodKey, slavePod); err != nil {
			return fmt.Errorf("failed to get slave pod %s: %v", slaveName, err)
		}

		// 配置主从复制: 先停slave，再配置、然后再启slave
		masterServiceName := cluster.Spec.MasterService
		slaveCommand := fmt.Sprintf(
			"mysql -uroot -p%s -e \"STOP SLAVE;CHANGE MASTER TO MASTER_HOST='%s', MASTER_USER='replica', MASTER_PASSWORD='password', MASTER_AUTO_POSITION=1; START SLAVE;\"",
			MySQLPassword,
			masterServiceName,
		)
		if _, err := r.execCommandOnPod(slavePod, slaveCommand); err != nil {
			return fmt.Errorf("failed to execute command on slave pod %s: %v", slaveName, err)
		}

		// 打标签
		if err := r.labelPod(ctx, slaveName, "slave", cluster); err != nil {
			return fmt.Errorf("failed to label slave pod %s: %v", slaveName, err)
		}
	}

	return nil
}

// labelPod 为 Pod 打标签
func (r *MysqlClusterReconciler) labelPod(ctx context.Context, podName, role string, cluster databasev1.MysqlCluster) error {
	pod := &v1.Pod{}
	podKey := client.ObjectKey{Namespace: cluster.Namespace, Name: podName}
	if err := r.Get(ctx, podKey, pod); err != nil {
		return fmt.Errorf("failed to get pod %s: %v", podName, err)
	}

	// 更新 Pod 标签
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}
	pod.Labels["role"] = role

	if err := r.Update(ctx, pod); err != nil {
		return fmt.Errorf("failed to update pod %s: %v", podName, err)
	}

	return nil
}

// kubectl exec 进入pod内执行命令
func (r *MysqlClusterReconciler) execCommandOnPod(pod *v1.Pod, command string) (string, error) {
	// Load kubeconfig from default location
	//kubeconfig := os.Getenv("KUBECONFIG")
	//if kubeconfig == "" {
	//	kubeconfig = "/root/.kube/config" // Fallback to default path
	//}
	config, err := clientcmd.BuildConfigFromFlags("", KubeConfigPath) // 来自包："k8s.io/client-go/tools/clientcmd"
	// config, err := rest.InClusterConfig() // 来自包："k8s.io/client-go/rest"

	if err != nil {
		return "", err
	}

	// Create a new Kubernetes clientset
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return "", err
	}

	// Create REST client for pod exec
	restClient := kubeClient.CoreV1().RESTClient()
	req := restClient.
		Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec").
		Param("stdin", "false").
		Param("stdout", "true").
		Param("stderr", "true").
		Param("tty", "false").
		Param("container", pod.Spec.Containers[0].Name).
		Param("command", "/bin/sh").
		Param("command", "-c").
		Param("command", command)

	// Create an executor
	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return "", err
	}

	// Execute the command
	var output strings.Builder
	err = executor.Stream(remotecommand.StreamOptions{
		Stdout: &output,
		Stderr: os.Stderr,
	})
	if err != nil {
		return "", err
	}

	return output.String(), nil
}

// 通过master-service中的endpoint地址获取当前主库pod名
func (r *MysqlClusterReconciler) getMasterPodNameFromEndpoints(ctx context.Context, cluster databasev1.MysqlCluster) (string, error) {
	log := log.FromContext(ctx)

	// 获取 master-service 关联的 Pod 名称
	endpoints := &v1.Endpoints{}
	if err := r.Get(ctx, client.ObjectKey{Name: cluster.Spec.MasterService, Namespace: cluster.Namespace}, endpoints); err != nil {
		return "", fmt.Errorf("failed to get endpoints for service %s: %v", cluster.Spec.MasterService, err)
	}

	if len(endpoints.Subsets) == 0 || len(endpoints.Subsets[0].Addresses) == 0 {
		return "", fmt.Errorf("no endpoints found for service %s", cluster.Spec.MasterService)
	}

	masterPodName := endpoints.Subsets[0].Addresses[0].TargetRef.Name
	log.Info("从master-service的endpoints获取主pod名", "masterPodName", masterPodName)

	// 返回第一个 Pod 名称
	return masterPodName, nil
}

// 获取所有当前从pod的名字
func (r *MysqlClusterReconciler) getReplicaPodsNames(ctx context.Context, cluster databasev1.MysqlCluster, masterPodName string) ([]string, error) {
	// 获取所有实际副本信息
	actualReplicaCount, podNames := r.getActualReplicaInfo(ctx, cluster)

	// 如果没有实际副本，直接返回空数组
	if actualReplicaCount == 0 {
		return nil, nil
	}

	// 获取主库 Pod 名称
	//masterPodName, err := r.getMasterPodNameFromEndpoints(ctx, cluster)
	//if err != nil {
	//	return nil, err
	//}

	// 过滤掉主库 Pod 名称，得到从库 Pod 名称
	var replicaPodNames []string
	for _, podName := range podNames {
		if podName != masterPodName {
			replicaPodNames = append(replicaPodNames, podName)
		}
	}

	return replicaPodNames, nil
}
