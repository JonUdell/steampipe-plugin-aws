package aws

import (
	"context"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/turbot/steampipe-plugin-sdk/v2/grpc/proto"
	"github.com/turbot/steampipe-plugin-sdk/v2/plugin"
	"github.com/turbot/steampipe-plugin-sdk/v2/plugin/transform"
)

//// TABLE DEFINITION

func tableAwsEcrRepository(_ context.Context) *plugin.Table {
	return &plugin.Table{
		Name:        "aws_ecr_repository",
		Description: "AWS ECR Repository",
		Get: &plugin.GetConfig{
			KeyColumns:        plugin.SingleColumn("repository_name"),
			ShouldIgnoreError: isNotFoundError([]string{"RepositoryNotFoundException", "RepositoryPolicyNotFoundException", "LifecyclePolicyNotFoundException"}),
			Hydrate:           getAwsEcrRepositories,
		},
		List: &plugin.ListConfig{
			Hydrate: listAwsEcrRepositories,
			KeyColumns: []*plugin.KeyColumn{
				{Name: "registry_id", Require: plugin.Optional},
			},
		},
		GetMatrixItem: BuildRegionList,
		Columns: awsRegionalColumns([]*plugin.Column{
			{
				Name:        "repository_name",
				Description: "The name of the repository.",
				Type:        proto.ColumnType_STRING,
			},
			{
				Name:        "registry_id",
				Description: "The AWS account ID associated with the registry that contains the repositories to be described.",
				Type:        proto.ColumnType_STRING,
			},
			{
				Name:        "arn",
				Description: "The Amazon Resource Name (ARN) that identifies the repository.",
				Type:        proto.ColumnType_STRING,
				Transform:   transform.FromField("RepositoryArn"),
			},
			{
				Name:        "repository_uri",
				Description: "The URI for the repository.",
				Type:        proto.ColumnType_STRING,
			},
			{
				Name:        "created_at",
				Description: "The date and time, in JavaScript date format, when the repository was created.",
				Type:        proto.ColumnType_TIMESTAMP,
			},
			{
				Name:        "image_tag_mutability",
				Description: "The tag mutability setting for the repository.",
				Type:        proto.ColumnType_STRING,
			},
			{
				Name:        "last_evaluated_at",
				Description: "The time stamp of the last time that the lifecycle policy was run.",
				Hydrate:     getAwsEcrRepositoryLifecyclePolicy,
				Type:        proto.ColumnType_TIMESTAMP,
			},
			{
				Name:        "max_results",
				Description: "The maximum number of repository results returned by DescribeRepositories.",
				Hydrate:     getAwsEcrRepositories,
				Type:        proto.ColumnType_INT,
			},
			{
				Name:        "encryption_configuration",
				Description: "The encryption configuration for the repository.",
				Hydrate:     getAwsEcrRepositories,
				Type:        proto.ColumnType_JSON,
			},
			{
				Name:        "image_details",
				Description: "A list of ImageDetail objects that contain data about the image.",
				Hydrate:     getAwsEcrDescribeImages,
				Type:        proto.ColumnType_JSON,
			},
			{
				Name:        "image_scanning_configuration",
				Description: "The image scanning configuration for a repository.",
				Type:        proto.ColumnType_JSON,
			},
			{
				Name:        "lifecycle_policy",
				Description: "The JSON lifecycle policy text.",
				Hydrate:     getAwsEcrRepositoryLifecyclePolicy,
				Type:        proto.ColumnType_JSON,
				Transform:   transform.FromField("LifecyclePolicyText"),
			},
			{
				Name:        "policy",
				Description: "The JSON repository policy text associated with the repository.",
				Hydrate:     getAwsEcrRepositoryPolicy,
				Type:        proto.ColumnType_JSON,
				Transform:   transform.FromField("PolicyText"),
			},
			{
				Name:        "policy_std",
				Description: "Contains the policy in a canonical form for easier searching.",
				Hydrate:     getAwsEcrRepositoryPolicy,
				Type:        proto.ColumnType_JSON,
				Transform:   transform.FromField("PolicyText").Transform(unescape).Transform(policyToCanonical),
			},
			{
				Name:        "tags_src",
				Description: "A list of tags assigned to the Repository.",
				Type:        proto.ColumnType_JSON,
				Hydrate:     listAwsEcrRepositoryTags,
				Transform:   transform.FromField("Tags"),
			},

			// Standard columns for all tables
			{
				Name:        "tags",
				Description: resourceInterfaceDescription("tags"),
				Type:        proto.ColumnType_JSON,
				Hydrate:     listAwsEcrRepositoryTags,
				Transform:   transform.FromField("Tags").Transform(ecrTagListToTurbotTags),
			},
			{
				Name:        "title",
				Description: resourceInterfaceDescription("title"),
				Type:        proto.ColumnType_STRING,
				Transform:   transform.FromField("RepositoryName"),
			},
			{
				Name:        "akas",
				Description: resourceInterfaceDescription("akas"),
				Type:        proto.ColumnType_JSON,
				Transform:   transform.FromField("RepositoryArn").Transform(arnToAkas),
			},
		}),
	}
}

//// LIST FUNCTION

func listAwsEcrRepositories(ctx context.Context, d *plugin.QueryData, _ *plugin.HydrateData) (interface{}, error) {
	// Create Session
	svc, err := EcrService(ctx, d)
	if err != nil {
		return nil, err
	}

	input := &ecr.DescribeRepositoriesInput{
		MaxResults: aws.Int64(1000),
	}

	equalQuals := d.KeyColumnQuals
	if equalQuals["registry_id"] != nil {
		input.RegistryId = aws.String(equalQuals["registry_id"].GetStringValue())
	}

	limit := d.QueryContext.Limit
	if d.QueryContext.Limit != nil {
		if *limit < *input.MaxResults {
			if *limit < 5 {
				input.MaxResults = aws.Int64(5)
			} else {
				input.MaxResults = limit
			}
		}
	}

	// List call
	err = svc.DescribeRepositoriesPages(
		input,
		func(page *ecr.DescribeRepositoriesOutput, isLast bool) bool {
			for _, repository := range page.Repositories {
				d.StreamListItem(ctx, repository)

				// Context may get cancelled due to manual cancellation or if the limit has been reached
				if d.QueryStatus.RowsRemaining(ctx) == 0 {
					return false
				}
			}
			return !isLast
		},
	)

	return nil, err
}

////  HYDRATE FUNCTIONS

func getAwsEcrRepositories(ctx context.Context, d *plugin.QueryData, h *plugin.HydrateData) (interface{}, error) {
	logger := plugin.Logger(ctx)
	logger.Trace("getAwsEcrRepositories")

	var name string
	if h.Item != nil {
		name = *h.Item.(*ecr.Repository).RepositoryName
	} else {
		name = d.KeyColumnQuals["repository_name"].GetStringValue()
	}

	// Create Session
	svc, err := EcrService(ctx, d)
	if err != nil {
		return nil, err
	}

	// Build the params
	params := &ecr.DescribeRepositoriesInput{
		RepositoryNames: []*string{aws.String(name)},
	}

	// Get call
	data, err := svc.DescribeRepositories(params)
	if err != nil {
		logger.Debug("getAwsEcrRepositories", "ERROR", err)
		return nil, err
	}
	return data.Repositories[0], nil
}

func listAwsEcrRepositoryTags(ctx context.Context, d *plugin.QueryData, h *plugin.HydrateData) (interface{}, error) {
	logger := plugin.Logger(ctx)
	logger.Trace("listAwsEcrRepositoryTags")

	resourceArn := h.Item.(*ecr.Repository).RepositoryArn

	// Create Session
	svc, err := EcrService(ctx, d)
	if err != nil {
		return nil, err
	}

	// Build the params
	params := &ecr.ListTagsForResourceInput{
		ResourceArn: resourceArn,
	}

	// Get call
	op, err := svc.ListTagsForResource(params)
	if err != nil {
		logger.Debug("listAwsEcrRepositoryTags", "ERROR", err)
		return nil, err
	}
	return op, nil
}

func getAwsEcrRepositoryPolicy(ctx context.Context, d *plugin.QueryData, h *plugin.HydrateData) (interface{}, error) {
	logger := plugin.Logger(ctx)
	logger.Trace("getAwsEcrRepositoryPolicy")

	repositoryName := h.Item.(*ecr.Repository).RepositoryName

	// Create Session
	svc, err := EcrService(ctx, d)
	if err != nil {
		return nil, err
	}

	// Build the params
	params := &ecr.GetRepositoryPolicyInput{
		RepositoryName: repositoryName,
	}

	// Get call
	op, err := svc.GetRepositoryPolicy(params)
	if err != nil {
		if a, ok := err.(awserr.Error); ok {
			if a.Code() == "RepositoryPolicyNotFoundException" {
				return nil, nil
			}
			return nil, err
		}
	}
	return op, nil
}

func getAwsEcrDescribeImages(ctx context.Context, d *plugin.QueryData, h *plugin.HydrateData) (interface{}, error) {
	logger := plugin.Logger(ctx)
	logger.Trace("getAwsEcrDescribeImages")

	repositoryName := h.Item.(*ecr.Repository).RepositoryName

	// Create Session
	svc, err := EcrService(ctx, d)
	if err != nil {
		return nil, err
	}

	// Build the params
	params := &ecr.DescribeImagesInput{
		RepositoryName: repositoryName,
	}

	// Get call
	op, err := svc.DescribeImages(params)
	if err != nil {
		logger.Debug("getAwsEcrDescribeImages", "ERROR", err)
		return nil, err
	}

	return op, nil
}

func getAwsEcrRepositoryLifecyclePolicy(ctx context.Context, d *plugin.QueryData, h *plugin.HydrateData) (interface{}, error) {
	logger := plugin.Logger(ctx)
	logger.Trace("getAwsEcrRepositoryLifecyclePolicy")

	repositoryName := h.Item.(*ecr.Repository).RepositoryName

	// Create Session
	svc, err := EcrService(ctx, d)
	if err != nil {
		return nil, err
	}
	// Build the params
	params := &ecr.GetLifecyclePolicyInput{
		RepositoryName: repositoryName,
	}
	// Get call
	op, err := svc.GetLifecyclePolicy(params)
	if err != nil {
		if a, ok := err.(awserr.Error); ok {
			if a.Code() == "LifecyclePolicyNotFoundException" {
				return nil, nil
			}
			return nil, err
		}
	}
	return op, nil
}

//// TRANSFORM FUNCTIONS

func ecrTagListToTurbotTags(ctx context.Context, d *transform.TransformData) (interface{}, error) {
	plugin.Logger(ctx).Trace("ecrTagListToTurbotTags")
	tags := d.HydrateItem.(*ecr.ListTagsForResourceOutput)

	// Mapping the resource tags inside turbotTags
	var turbotTagsMap map[string]string
	if tags != nil {
		turbotTagsMap = map[string]string{}
		for _, i := range tags.Tags {
			turbotTagsMap[*i.Key] = *i.Value
		}
	}

	return turbotTagsMap, nil
}
