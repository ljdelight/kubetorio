/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	v1 "k8s.io/api/core/v1"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	kubetorio "github.com/ljdelight/kubetorio/api/v1beta1"
	kapps "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	jobOwnerKey = ".metadata.controller"
	apiGVStr    = kubetorio.GroupVersion.String()
)

// ServerReconciler reconciles a Server object
type ServerReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kubetorio.ljdelight.com,resources=servers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubetorio.ljdelight.com,resources=servers/status,verbs=get;update;patch

func (r *ServerReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("server", req.NamespacedName)

	var kubetorioServer kubetorio.Server
	if err := r.Get(ctx, req.NamespacedName, &kubetorioServer); err != nil {
		log.Error(err, "unable to fetch kubetorioServer")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// NOTE: This listing works because `SetupWithManager` specifies that `Deployments` are generated from this controller and should be monitored.
	var childDeployments kapps.DeploymentList
	if err := r.List(ctx, &childDeployments, client.InNamespace(req.Namespace), client.MatchingField(jobOwnerKey, req.Name)); err != nil {
		log.Error(err, "unable to list child Jobs", "req", req)
		return ctrl.Result{}, err
	}

	deployment, e := r.makeDeployment(&kubetorioServer)
	if e != nil {
		log.Error(e, "unable to construct deployment")
		return ctrl.Result{}, e
	}

	if err := r.Create(ctx, deployment); err != nil {
		log.Error(err, "unable to create deployment", "deployment", deployment)
		return ctrl.Result{}, err
	}

	log.V(1).Info("create deployment for run", "deployment", deployment)
	return ctrl.Result{}, nil
}

func int32Ptr(i int32) *int32 { return &i }

func (r *ServerReconciler) makeDeployment(server *kubetorio.Server) (*kapps.Deployment, error) {
	deployment := &kapps.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "factorio",
			Namespace: "default",
			Labels:    map[string]string{"app": "factorio"},
		},
		Spec: kapps.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "factorio"},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "factorio"},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "factorio",
							Image: "factoriotools/factorio:0.17.58",
							Env: []v1.EnvVar{
								{Name: "PORT", Value: "31001"},
								{Name: "RCON_PORT", Value: "27015"},
							},
							Command: []string{"/bin/sh", "-cxe"},
							Args: []string{`
								jq '.visibility.public = "false"' /opt/factorio/data/server-settings.example.json > /opt/factorio/data/server-settings.json.tmp;
								mv /opt/factorio/data/server-settings.json.tmp /opt/factorio/data/server-settings.example.json;
								cat /opt/factorio/data/server-settings.example.json;
								exec /docker-entrypoint.sh;
							`},
						},
					},
				},
			},
		},
	}
	return deployment, nil
}

func (r *ServerReconciler) SetupWithManager(mgr ctrl.Manager) error {

	if err := mgr.GetFieldIndexer().IndexField(&kapps.Deployment{}, jobOwnerKey, func(rawObj runtime.Object) []string {
		// grab the job object, extract the owner...
		deployment := rawObj.(*kapps.Deployment)
		owner := metav1.GetControllerOf(deployment)
		if owner == nil {
			return nil
		}
		// ...make sure it's a CronJob...
		if owner.APIVersion != apiGVStr || owner.Kind != "Server" {
			r.Log.Info("Indexer found owner for this controller but it was not a server", "ownerController", owner)
			return nil
		}

		r.Log.Info("Found controller owner", "owner", owner)
		return []string{owner.Name}
	}); err != nil {
		r.Log.Error(err, "Failed to setup indexer")
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&kubetorio.Server{}).
		Owns(&kapps.Deployment{}).
		Complete(r)
}
