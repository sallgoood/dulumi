package dulumi

import (
	"fmt"
	plm "github.com/pulumi/pulumi/sdk/v2/go/pulumi"
	"github.com/sallgoood/dulumi/utils"
)

func NewS3StaticWebInfra(ctx *plm.Context,
	host string,
	domain string,
	service string,
	env string,
	domainSslCertArn string,
	gitBranch string,
	gitPolling bool,
	requireApproval bool,
	requireNoti bool,
	buildSpec string,) error {

	utils.RegisterAutoTags(ctx, plm.StringMap{
		"Environment": plm.String(env),
		"Name":        plm.String(service),
	})

	dsw, err := NewS3StaticWeb(ctx, host, domain, fmt.Sprintf("%v-%v", service, env), domainSslCertArn)
	if err != nil {
		return err
	}

	_, err = NewS3StaticWebCICD(
		ctx,
		fmt.Sprintf("%v", dsw.BucketName),
		fmt.Sprintf("%v-%v", service, env),
		"arn:aws:iam::784015586554:role/service-role/codebuild-service-role",
		"arn:aws:iam::784015586554:role/AWS-CodePipeline-Service",
		service,
		gitBranch,
		gitPolling,
		requireApproval,
		requireNoti,
		buildSpec,
	)
	if err != nil {
		return err
	}

	ctx.Export("bucketName", dsw.BucketName)
	return nil
}

func NewFargateApiInfra(
	ctx *plm.Context,
	service string,
	env string,
	taskSubnetIds []string,
	taskSecurityGroupIds []string,
	subnetIds []string,
	securityGroupIds []string,
	vpcId string,
	taskRole string,
	appPort int,
	appCpu string,
	appMemory string,
	appHealthCheckPath string,
	domain string,
	subdomain string,
	certificateArn string,
	scaleMax int,
	scaleMin int,
	scaleCpuPercent float64,
	containerDefinitions string,
	buildRole string,
	pipelineRole string,
	gitRepo string,
	gitBranch string,
	gitPolling bool,
	requireApproval bool,
	requireNoti bool,
	buildSpec string,
	opts ...plm.ResourceOption,
) (*FargateApi, *FargateApiCICD, error) {

	api, err := NewFargateApi(
		ctx,
		service,
		env,
		taskSubnetIds,
		taskSecurityGroupIds,
		subnetIds,
		securityGroupIds,
		vpcId,
		taskRole,
		appPort,
		appCpu,
		appMemory,
		appHealthCheckPath,
		domain,
		subdomain,
		certificateArn,
		scaleMax,
		scaleMin,
		scaleCpuPercent,
		containerDefinitions,
		opts...
	)
	if err != nil {
		return nil, nil, err
	}

	cicd, err := NewFargateApiCICD(
		ctx,
		fmt.Sprintf("%v-%v", service, env),
		buildRole,
		pipelineRole,
		gitRepo,
		gitBranch,
		gitPolling,
		requireApproval,
		requireNoti,
		buildSpec,
		service,
		fmt.Sprintf("%v-%v", service, env),
	)
	if err != nil {
		return nil, nil, err
	}

	ctx.Export("dns", api.Dns)
	return api, cicd, nil
}
