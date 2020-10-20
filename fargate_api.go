package dulumi

import (
	"fmt"
	aas "github.com/pulumi/pulumi-aws/sdk/v3/go/aws/appautoscaling"
	"github.com/pulumi/pulumi-aws/sdk/v3/go/aws/ecs"
	alb "github.com/pulumi/pulumi-aws/sdk/v3/go/aws/lb"
	plm "github.com/pulumi/pulumi/sdk/v2/go/pulumi"
	"github.com/sallgoood/dulumi/utils"
	"log"
)

type FargateApi struct {
	plm.ResourceState

	Dns plm.StringOutput `pulumi:"dnsName"`
}

func NewFargateApi(ctx *plm.Context,
	service string,
	env string,
	subnetIds []string,
	securityGroupIds []string,
	vpcId string,
	taskRole string,
	appPort int,
	appCpu string,
	appMemory string,
	appHealthCheckPath string,
	certificateArn string,
	scaleMax int,
	scaleMin int,
	scaleCpuPercent float64,
	opts ...plm.ResourceOption,
) (*FargateApi, error) {

	if appPort == 0 {
		appPort = 80
	}

	var dfa FargateApi
	err := ctx.RegisterComponentResource("drama:server:fargate-api", "drama-fargate-api", &dfa, opts...)
	if err != nil {
		return nil, err
	}

	var ecsClusterName string
	var ecsClusterArn string

	existCluster, err := ecs.LookupCluster(ctx, &ecs.LookupClusterArgs{
		ClusterName: service,
	})
	if err != nil {
		log.Printf("ecs cluster, %v does not exists, details : %v", service, err)
		cluster, err := ecs.NewCluster(ctx, service, &ecs.ClusterArgs{
			Name: plm.StringPtr(service),
			Settings: ecs.ClusterSettingArray{
				ecs.ClusterSettingArgs{
					Name:  plm.String("containerInsights"),
					Value: plm.String("enabled"),
				},
			},
		}, plm.Parent(&dfa),
			plm.Protect(true))
		if err != nil {
			return nil, err
		}

		ecsClusterName = fmt.Sprintf("%v", cluster.Name)
		ecsClusterArn = fmt.Sprintf("%v", cluster.Arn)

	} else {
		ecsClusterName = existCluster.ClusterName
		ecsClusterArn = existCluster.Arn
	}

	targetGroup, listener, err := apiAlb(ctx, service, env, subnetIds, securityGroupIds, vpcId, appPort, appHealthCheckPath, certificateArn, dfa)
	if err != nil {
		return nil, err
	}

	initialTask, err := ecs.NewTaskDefinition(ctx, "app-task", &ecs.TaskDefinitionArgs{
		Family:                  plm.String("fargate-task-definition"),
		Cpu:                     plm.String(appCpu),
		Memory:                  plm.String(appMemory),
		NetworkMode:             plm.String("awsvpc"),
		RequiresCompatibilities: plm.StringArray{plm.String("FARGATE")},
		ExecutionRoleArn:        plm.String(taskRole),
		ContainerDefinitions:    plm.String(containerTemplate()),
	}, plm.Parent(&dfa))
	if err != nil {
		return nil, err
	}

	svc, err := ecs.NewService(ctx, "app-svc", &ecs.ServiceArgs{
		Name:           plm.String(fmt.Sprintf("%v-%v", service, env)),
		Cluster:        plm.String(ecsClusterArn),
		TaskDefinition: initialTask.Arn,
		DesiredCount:   plm.Int(1),
		LaunchType:     plm.String("FARGATE"),
		DeploymentController: ecs.ServiceDeploymentControllerArgs{
			Type: plm.StringPtr("ECS"),
		},
		NetworkConfiguration: &ecs.ServiceNetworkConfigurationArgs{
			AssignPublicIp: plm.Bool(true),
			Subnets:        utils.ToPulumiStringArray(subnetIds),
			SecurityGroups: utils.ToPulumiStringArray(securityGroupIds),
		},
		LoadBalancers: ecs.ServiceLoadBalancerArray{
			ecs.ServiceLoadBalancerArgs{
				TargetGroupArn: targetGroup.Arn,
				ContainerName:  plm.String("app"),
				ContainerPort:  plm.Int(appPort),
			},
		},
	}, plm.DependsOn([]plm.Resource{listener}),
		plm.IgnoreChanges([]string{"taskDefinition", "desiredCount"}),
		plm.Parent(&dfa))
	if err != nil {
		return nil, err
	}

	autoscaleResourceId := plm.String(fmt.Sprintf("service/%v/%v", plm.String(ecsClusterName), svc.Name))

	_, err = aas.NewTarget(ctx, "autoscaleTarget", &aas.TargetArgs{
		MaxCapacity:       plm.Int(scaleMax),
		MinCapacity:       plm.Int(scaleMin),
		ResourceId:        autoscaleResourceId,
		ScalableDimension: plm.String("ecs:service:DesiredCount"),
		ServiceNamespace:  plm.String("ecs"),
	}, plm.Parent(&dfa))
	if err != nil {
		return nil, err
	}

	_, err = aas.NewPolicy(ctx, "autoscalePolicy", &aas.PolicyArgs{
		Name:              plm.String("scale-inout"),
		PolicyType:        plm.String("TargetTrackingScaling"),
		ResourceId:        autoscaleResourceId,
		ScalableDimension: plm.String("ecs:service:DesiredCount"),
		ServiceNamespace:  plm.String("ecs"),
		TargetTrackingScalingPolicyConfiguration: aas.PolicyTargetTrackingScalingPolicyConfigurationArgs{
			CustomizedMetricSpecification: nil,
			DisableScaleIn:                nil,
			PredefinedMetricSpecification: aas.PolicyTargetTrackingScalingPolicyConfigurationPredefinedMetricSpecificationArgs{
				PredefinedMetricType: plm.String("ECSServiceAverageCPUUtilization"),
			},
			ScaleInCooldown:  plm.IntPtr(30),
			ScaleOutCooldown: plm.IntPtr(1),
			TargetValue:      plm.Float64(scaleCpuPercent),
		},
	}, plm.Parent(&dfa))
	if err != nil {
		return nil, err
	}

	return &dfa, nil
}

func apiAlb(
	ctx *plm.Context,
	service string,
	env string,
	subnetIds []string,
	securityGroupIds []string,
	vpcId string,
	appPort int,
	appHealthCheckPath string,
	certificateArn string,
	dfa FargateApi,
) (*alb.TargetGroup, *alb.Listener, error) {
	lb, err := alb.NewLoadBalancer(ctx, "lb", &alb.LoadBalancerArgs{
		Subnets:        utils.ToPulumiStringArray(subnetIds),
		SecurityGroups: utils.ToPulumiStringArray(securityGroupIds),
	}, plm.Parent(&dfa))
	if err != nil {
		return nil, nil, err
	}
	tg, err := alb.NewTargetGroup(ctx, "tg", &alb.TargetGroupArgs{
		Name:                plm.String(fmt.Sprintf("%v-%v", service, env)),
		Port:                plm.Int(appPort),
		Protocol:            plm.String("HTTP"),
		TargetType:          plm.String("ip"),
		VpcId:               plm.String(vpcId),
		DeregistrationDelay: plm.Int(1),
		HealthCheck: alb.TargetGroupHealthCheckArgs{
			Enabled:            plm.BoolPtr(true),
			HealthyThreshold:   plm.IntPtr(3),
			UnhealthyThreshold: plm.IntPtr(3),
			Interval:           plm.IntPtr(30),
			Matcher:            plm.StringPtr("200-299"),
			Path:               plm.StringPtr(appHealthCheckPath),
			Port:               plm.StringPtr(string(rune(appPort))),
			Protocol:           plm.StringPtr("HTTP"),
			Timeout:            plm.IntPtr(5),
		},
	}, plm.Parent(&dfa))
	if err != nil {
		return nil, nil, err
	}
	_, err = alb.NewListener(ctx, "httpListener", &alb.ListenerArgs{
		LoadBalancerArn: lb.Arn,
		Port:            plm.Int(80),
		CertificateArn:  plm.StringPtr(certificateArn),
		DefaultActions: alb.ListenerDefaultActionArray{
			alb.ListenerDefaultActionArgs{
				Type: plm.String("redirect"),
				Redirect: alb.ListenerDefaultActionRedirectArgs{
					Port:       plm.StringPtr("443"),
					Protocol:   plm.StringPtr("HTTPS"),
					StatusCode: plm.String("HTTP_301"),
				},
			},
		},
	}, plm.Parent(&dfa))
	if err != nil {
		return nil, nil, err
	}
	https, err := alb.NewListener(ctx, "httpSListener", &alb.ListenerArgs{
		LoadBalancerArn: lb.Arn,
		Port:            plm.Int(443),
		DefaultActions: alb.ListenerDefaultActionArray{
			alb.ListenerDefaultActionArgs{
				Type:           plm.String("forward"),
				TargetGroupArn: tg.Arn,
			},
		},
	}, plm.Parent(&dfa))
	if err != nil {
		return nil, nil, err
	}

	if err = ctx.RegisterResourceOutputs(&dfa, plm.Map{
		"dns": lb.DnsName,
	}); err != nil {
		return nil, nil, err
	}

	return tg, https, nil
}

func containerTemplate() string {
	return fmt.Sprintf(`[
  {
    "name": "app",
    "image": "nginx:latest",
    "portMappings": [
      {
        "containerPort": 80,
        "hostPort": 80,
        "protocol": "tcp"
      }
    ],
    "environment": [],
    "ulimits": [{
      "name": "nofile",
      "softLimit": 65535,
      "hardLimit": 65535
    }],
    "healthCheck": {
      "retries": 3,
      "command": [
        "CMD-SHELL",
        "echo hello"
      ],
      "timeout": 5,
      "interval": 30
    },
    "logConfiguration": {
      "logDriver": "awsfirelens"
    },
    "essential": true
  },
  {
    "logConfiguration": {
      "logDriver": "awslogs",
      "options": {
        "awslogs-group": "%v",
        "awslogs-region": "ap-northeast-1",
        "awslogs-stream-prefix": "fluentbit"
      }
    },
    "portMappings": [
      {
        "hostPort": 24224,
        "protocol": "tcp",
        "containerPort": 24224
      }
    ],
    "cpu": 0,
    "environment": [],
    "mountPoints": [],
    "volumesFrom": [],
    "image": "906394416424.dkr.ecr.us-west-2.amazonaws.com/aws-for-fluent-bit:latest",
    "firelensConfiguration":{
        "type":"fluentbit",
        "options":{
           "config-file-type":"s3",
           "config-file-value":"arn:aws:s3:::drama-terraform-state/fluent-bit.conf"
         }
     },
    "user": "0",
    "name": "log-router"
  }
]`)
}
