// Copyright 2021 VMware
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/vmware-tanzu/cartographer/pkg/apis/v1alpha1"
	"github.com/vmware-tanzu/cartographer/pkg/conditions"
	"github.com/vmware-tanzu/cartographer/pkg/controller/deliverable"
	"github.com/vmware-tanzu/cartographer/pkg/controller/delivery"
	"github.com/vmware-tanzu/cartographer/pkg/controller/runnable"
	"github.com/vmware-tanzu/cartographer/pkg/controller/supplychain"
	"github.com/vmware-tanzu/cartographer/pkg/controller/workload"
	realizerclient "github.com/vmware-tanzu/cartographer/pkg/realizer/client"
	realizerdeliverable "github.com/vmware-tanzu/cartographer/pkg/realizer/deliverable"
	realizerrunnable "github.com/vmware-tanzu/cartographer/pkg/realizer/runnable"
	realizerworkload "github.com/vmware-tanzu/cartographer/pkg/realizer/workload"
	"github.com/vmware-tanzu/cartographer/pkg/repository"
	"github.com/vmware-tanzu/cartographer/pkg/tracker/dependency"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"time"
)

const defaultResyncTime = 10 * time.Hour

type Command struct {
	Port    int
	CertDir string
	Logger  logr.Logger
}

func (cmd *Command) Execute(ctx context.Context) error {
	log.SetLogger(cmd.Logger)
	l := log.Log.WithName("cartographer")

	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("get config: %w", err)
	}

	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		return fmt.Errorf("add to scheme: %w", err)
	}

	mgr, err := manager.New(cfg, manager.Options{
		Port:               cmd.Port,
		CertDir:            cmd.CertDir,
		Scheme:             scheme,
		MetricsBindAddress: "0",
	})

	if err != nil {
		return fmt.Errorf("failed to create new manager: %w", err)
	}

	if err := registerControllers(mgr); err != nil {
		return fmt.Errorf("failed to register controllers: %w", err)
	}

	// TODO: what is this?
	if err := IndexResources(ctx, mgr); err != nil {
		return fmt.Errorf("index resources: %w", err)
	}

	if cmd.CertDir == "" {
		l.Info("Not registering the webhook server. Must pass a directory containing tls.crt and tls.key to --cert-dir")
	} else {
		if err = (&v1alpha1.Workload{}).SetupWebhookWithManager(mgr); err != nil {
			return fmt.Errorf("workload webhook: %w", err)
		}
		if err := controllerruntime.NewWebhookManagedBy(mgr).
			For(&v1alpha1.ClusterSupplyChain{}).
			Complete(); err != nil {
			return fmt.Errorf("clustersupplychain webhook: %w", err)
		}
		if err := controllerruntime.NewWebhookManagedBy(mgr).
			For(&v1alpha1.ClusterConfigTemplate{}).
			Complete(); err != nil {
			return fmt.Errorf("clusterconfigtemplate webhook: %w", err)
		}
		if err := controllerruntime.NewWebhookManagedBy(mgr).
			For(&v1alpha1.ClusterImageTemplate{}).
			Complete(); err != nil {
			return fmt.Errorf("clusterimagetemplate webhook: %w", err)
		}
		if err := controllerruntime.NewWebhookManagedBy(mgr).
			For(&v1alpha1.ClusterSourceTemplate{}).
			Complete(); err != nil {
			return fmt.Errorf("clustersourcetemplate webhook: %w", err)
		}
		if err := controllerruntime.NewWebhookManagedBy(mgr).
			For(&v1alpha1.ClusterTemplate{}).
			Complete(); err != nil {
			return fmt.Errorf("clustertemplate webhook: %w", err)
		}
		if err := controllerruntime.NewWebhookManagedBy(mgr).
			For(&v1alpha1.ClusterDelivery{}).
			Complete(); err != nil {
			return fmt.Errorf("clusterdelivery webhook: %w", err)
		}
		if err := controllerruntime.NewWebhookManagedBy(mgr).
			For(&v1alpha1.ClusterDeploymentTemplate{}).
			Complete(); err != nil {
			return fmt.Errorf("clusterdeploymenttemplate webhook: %w", err)
		}
	}

	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("manager start: %w", err)
	}

	return nil
}
func AddToScheme(scheme *runtime.Scheme) error {
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("cartographer v1alpha1 add to scheme: %w", err)
	}

	if err := corev1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("core v1 add to scheme: %w", err)
	}

	if err := rbacv1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("rbac v1 add to scheme: %w", err)
	}

	return nil
}

func IndexResources(ctx context.Context, mgr manager.Manager) error {
	fieldIndexer := mgr.GetFieldIndexer()

	if err := indexSupplyChains(ctx, fieldIndexer); err != nil {
		return fmt.Errorf("index supply chain resource: %w", err)
	}

	return nil
}

func indexSupplyChains(ctx context.Context, fieldIndexer client.FieldIndexer) error {
	err := fieldIndexer.IndexField(ctx, &v1alpha1.ClusterSupplyChain{}, "spec.selector", v1alpha1.GetSelectorsFromObject)
	if err != nil {
		return fmt.Errorf("index field supply-chain.selector: %w", err)
	}

	return nil
}

func registerControllers(mgr manager.Manager) error {
	if err := registerWorkloadController(mgr); err != nil {
		return fmt.Errorf("failed to register workload controller: %w", err)
	}

	if err := registerSupplyChainController(mgr); err != nil {
		return fmt.Errorf("failed to register supply chain controller: %w", err)
	}

	if err := registerDeliverableController(mgr); err != nil {
		return fmt.Errorf("failed to register deliverable controller: %w", err)
	}

	if err := registerDeliveryController(mgr); err != nil {
		return fmt.Errorf("failed to register delivery controller: %w", err)
	}

	if err := registerRunnableController(mgr); err != nil {
		return fmt.Errorf("failed to register runnable controller: %w", err)
	}

	return nil
}

func registerWorkloadController(mgr manager.Manager) error {
	repo := repository.NewRepository(
		mgr.GetClient(),
		repository.NewCache(mgr.GetLogger().WithName("workload-repo-cache")),
	)

	reconciler := &workload.Reconciler{
		Repo:                    repo,
		ConditionManagerBuilder: conditions.NewConditionManager,
		ResourceRealizerBuilder: realizerworkload.NewResourceRealizerBuilder(repository.NewRepository, realizerclient.NewClientBuilder(mgr.GetConfig()), repository.NewCache(mgr.GetLogger().WithName("workload-stamping-repo-cache"))),
		Realizer:                realizerworkload.NewRealizer(),
		DependencyTracker:       dependency.NewDependencyTracker(2*defaultResyncTime, mgr.GetLogger().WithName("tracker-workload")),
	}

	err := reconciler.SetupWithManager(mgr)
	if err != nil {
		return fmt.Errorf("failed to setup with manager for workload: %w", err)
	}

	return nil
}

func registerSupplyChainController(mgr manager.Manager) error {
	repo := repository.NewRepository(
		mgr.GetClient(),
		repository.NewCache(mgr.GetLogger().WithName("supply-chain-repo-cache")),
	)

	reconciler := &supplychain.Reconciler{
		Repo:                    repo,
		ConditionManagerBuilder: conditions.NewConditionManager,
		DependencyTracker:       dependency.NewDependencyTracker(2*defaultResyncTime, mgr.GetLogger().WithName("tracker-supply-chain")),
	}

	err := reconciler.SetupWithManager(mgr)
	if err != nil {
		return fmt.Errorf("failed to setup with manager for supply chain: %w", err)
	}

	return nil
}

func registerDeliveryController(mgr manager.Manager) error {
	repo := repository.NewRepository(
		mgr.GetClient(),
		repository.NewCache(mgr.GetLogger().WithName("delivery-repo-cache")),
	)

	reconciler := &delivery.Reconciler{
		Repo:              repo,
		DependencyTracker: dependency.NewDependencyTracker(2*defaultResyncTime, mgr.GetLogger().WithName("tracker-delivery")),
	}

	err := reconciler.SetupWithManager(mgr)
	if err != nil {
		return fmt.Errorf("failed to setup with manager for delivery: %w", err)
	}

	return nil
}

func registerDeliverableController(mgr manager.Manager) error {
	repo := repository.NewRepository(
		mgr.GetClient(),
		repository.NewCache(mgr.GetLogger().WithName("deliverable-repo-cache")),
	)

	reconciler := &deliverable.Reconciler{
		Repo:                    repo,
		ConditionManagerBuilder: conditions.NewConditionManager,
		ResourceRealizerBuilder: realizerdeliverable.NewResourceRealizerBuilder(repository.NewRepository, realizerclient.NewClientBuilder(mgr.GetConfig()), repository.NewCache(mgr.GetLogger().WithName("deliverable-stamping-repo-cache"))),
		Realizer:                realizerdeliverable.NewRealizer(),
		DependencyTracker:       dependency.NewDependencyTracker(2*defaultResyncTime, mgr.GetLogger().WithName("tracker-deliverable")),
	}

	err := reconciler.SetupWithManager(mgr)
	if err != nil {
		return fmt.Errorf("failed to setup with manager for deliverable: %w", err)
	}

	return nil
}

func registerRunnableController(mgr manager.Manager) error {
	repo := repository.NewRepository(
		mgr.GetClient(),
		repository.NewCache(mgr.GetLogger().WithName("runnable-repo-cache")),
	)

	reconciler := &runnable.Reconciler{
		Repo:                    repo,
		Realizer:                realizerrunnable.NewRealizer(),
		RunnableCache:           repository.NewCache(mgr.GetLogger().WithName("runnable-stamping-repo-cache")),
		RepositoryBuilder:       repository.NewRepository,
		ClientBuilder:           realizerclient.NewClientBuilder(mgr.GetConfig()),
		ConditionManagerBuilder: conditions.NewConditionManager,
		DependencyTracker:       dependency.NewDependencyTracker(2*defaultResyncTime, mgr.GetLogger().WithName("tracker-runnable")),
	}

	err := reconciler.SetupWithManager(mgr)
	if err != nil {
		return fmt.Errorf("failed to setup with manager for runnable: %w", err)
	}

	return nil
}
