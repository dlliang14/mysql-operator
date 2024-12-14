# mysql-operator

k8s的mysql-operator，自动化部署mysql集群，自动切换主从，故障切换

## 介绍

### 功能特点

- **自动化部署**：通过简单的配置文件，快速部署 MySQL 集群。
- **弹性扩展**：根据需求自动扩展或缩减 MySQL 实例数量。
- **备份与恢复**：定时做gtid快照，能根据gtid做主从故障切换。

### 使用场景

- **开发与测试**：快速搭建 MySQL 环境，支持开发和测试工作。
- **学习k8s控制器开发**：可以自由定义控制器，目前加了3306探针，还能自己选择主从切换的逻辑和算法。

通过 Mysql-Operator，用户可以轻松管理 MySQL 集群，提升运维效率，确保数据可靠性和系统稳定性。

## Getting Started

### Prerequisites

- go version v1.22.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### 我的环境

- go version go1.23.2 linux/amd64
- Docker version 26.1.4, build 5650f9b
- Client Version: v1.30.3
- Kustomize Version: v5.0.4-0.20230601165947-6ce0bf390ce3
- Server Version: v1.30.3

### 集群内启动控制器

**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/mysql-operator:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/mysql-operator:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall

**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

### 集群外启动控制器

#### 修改代码

```shell
# 不在k8s集群内部，所以要指定kubeconfig
1. internal/controller/mysqlcluster_controller.go 文件中选择自己的kubeconfig路径

const (
  MySQLPassword          = "password"           // Hardcoded MySQL password
  KubeConfigPath         = "/root/.kube/config" // Hardcoded kubeconfig path 集群外需要取消注释
  MysqlClusterKind       = "MysqlCluster"
  MysqlClusterAPIVersion = "apps.egonlin.com/v1"
)

2. internal/controller/utlis.go 文件中包替换

  //"k8s.io/client-go/tools/clientcmd"   // 集群外部
  "k8s.io/client-go/rest"                 // 集群内部

  config, err := clientcmd.BuildConfigFromFlags("", KubeConfigPath) // 来自包："k8s.io/client-go/tools/clientcmd"
  // config, err := rest.InClusterConfig() // 来自包："k8s.io/client-go/rest"
```

#### 启动测试

**Install the CRDs into the cluster:**

```sh
make install
```

**启动控制器**

```sh
make run
```

**Create instances of your solution**

```sh
kubectl apply -k config/samples/
```

#### 清理环境

```sh
# local-path-stotrage.yaml 这个是本地存储控制器yaml 可以不用删除
# apps_v1_mysqlcluster.yaml 这个是Crd资源yaml 主要是删除这个
kubectl delete -f config/samples/apps_v1_mysqlcluster.yaml
make uninstall
# 查看pvc pv 删除包含mysql-的资源
for i in `kubectl get pvc| awk 'NR>1{print $1}'`;do echo $i; done
for i in `kubectl get pvc| awk 'NR>1&&/mysql-/{print $1}'`; do kubectl delete pvc $i; done
for i in `kubectl get pv| awk 'NR>1&&/mysql-/{print $1}'`; do kubectl delete pv $i; done
```

## Project Distribution

Following are the steps to build the installer and distribute this project to users.

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/mysql-operator:tag
```

NOTE: The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without
its dependencies.

2. Using the installer

Users can just run kubectl apply -f <URL for YAML BUNDLE> to install the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/mysql-operator/<tag or branch>/dist/install.yaml
```

## Contributing

// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2024 dlliang14.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
