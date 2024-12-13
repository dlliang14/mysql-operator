package controller

import (
	"context"                         // 用于处理上下文，提供超时、取消等操作
	"github.com/go-logr/logr"         // 用于记录日志
	"k8s.io/apimachinery/pkg/runtime" // 提供对象的通用机制，如序列化和版本转换

	"sigs.k8s.io/controller-runtime/pkg/client" // 提供与 Kubernetes API 交互的客户端
	"sigs.k8s.io/controller-runtime/pkg/log"

	databasev1 "github.com/dlliang14/api/v1" // 导入自定义的 MySQLCluster API 资源定义
	v1 "k8s.io/api/core/v1"                // 核心 Kubernetes API 对象，例如 Pod 和 Service
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	MySQLPassword          = "password"           // Hardcoded MySQL password
	//KubeConfigPath         = "/root/.kube/config" // Hardcoded kubeconfig path
	MysqlClusterKind       = "MysqlCluster"
	MysqlClusterAPIVersion = "apps.dlliang14.com/v1"
)

// MysqlClusterReconciler reconciles a MysqlCluster object
type MysqlClusterReconciler struct {
	client.Client                  // 嵌入 client.Client 接口，用于与 Kubernetes API 交互
	Log                logr.Logger // 日志记录器
	Scheme             *runtime.Scheme
	MasterGTIDSnapshot string // 用于存储主库的 GTID 快照
	SnapGoIsEnabled    bool   // 标识用于记录GTID快照的协程序是否启动，默认值为false，只有启动后才会设置为true
}

/*
在您的代码中，涉及到的 Kubernetes 资源包括：Pod、ConfigMap、Service、Endpoints、namespace对应设置权限如下
*/
// +kubebuilder:rbac:groups=apps.dlliang14.com,resources=mysqlclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.dlliang14.com,resources=mysqlclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.dlliang14.com,resources=mysqlclusters/finalizers,verbs=update

// +kubebuilder:rbac:groups="",resources=pods;services;configmaps,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=pods/exec,verbs=create;get;list;watch
// +kubebuilder:rbac:groups="",resources=endpoints,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumes,verbs=get;list;watch;create;update;delete

// 调谐函数
func (r *MysqlClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("调谐函数触发执行", "req", req) // 额外增加1个字段

	var cluster databasev1.MysqlCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 检查是否已经初始化
	if _, ok := cluster.Annotations["initialized"]; !ok {
		// 未初始，则调用初始化函数
		if err := r.init(ctx, &cluster); err != nil {
			log.Info("初始化集群失败")
			return ctrl.Result{}, err
		} else {
			log.Info("初始化集群成功")
		}

		// 设置 annotation 表示初始化已完成
		if cluster.Annotations == nil {
			cluster.Annotations = make(map[string]string)
		}
		cluster.Annotations["initialized"] = "true"
		if err := r.Update(ctx, &cluster); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		// 已经初始完成，则进入检测逻辑
		// 1、副本调谐
		result, err := r.reconcileReplicas(ctx, cluster)
		if err != nil {
			return result, err
		}
		// 2、主从检测逻辑与调谐
		result, err = r.reconcileMasterSlave(ctx, cluster)
		if err != nil {
			return result, err
		}

	}

	// 启用协程定期记录当前主库的GTID快照，用于选举依据
	if !r.SnapGoIsEnabled {
		r.startAndUpdateGTIDSnapshot(ctx, cluster)
		r.SnapGoIsEnabled = true
	}

	return ctrl.Result{}, nil
}

// 在cmd/main.go入口main函数中会调用该函数，来对你的控制器进行设置，指定控制器管理的资源，并将控制器注册到控制器管理器中
func (r *MysqlClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// 增加：Owns(&v1.Pod{})，确保检测pod资源
	return ctrl.NewControllerManagedBy(mgr).
		For(&databasev1.MysqlCluster{}).
		Owns(&v1.Pod{}).
		Complete(r)
}
