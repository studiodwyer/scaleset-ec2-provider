package interfaces

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

type EC2Client interface {
	RunInstances(ctx context.Context, input *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	TerminateInstances(ctx context.Context, input *ec2.TerminateInstancesInput, opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
}

type RealEC2Client struct {
	Client *ec2.Client
}

func (r *RealEC2Client) RunInstances(ctx context.Context, input *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	return r.Client.RunInstances(ctx, input, opts...)
}

func (r *RealEC2Client) TerminateInstances(ctx context.Context, input *ec2.TerminateInstancesInput, opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	return r.Client.TerminateInstances(ctx, input, opts...)
}
