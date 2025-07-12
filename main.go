package main

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/route53"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {

		vpc, err := ec2.NewVpc(ctx, "kafka-playground-vpc", &ec2.VpcArgs{
			CidrBlock:          pulumi.String("10.0.0.0/16"),
			EnableDnsHostnames: pulumi.Bool(true),
			EnableDnsSupport:   pulumi.Bool(true),
			Tags: pulumi.StringMap{
				"Name": pulumi.String("kafka-playground-vpc"),
			},
		})
		if err != nil {
			return err
		}

		availableAzs, err := aws.GetAvailabilityZones(ctx, &aws.GetAvailabilityZonesArgs{
			State: pulumi.StringRef("available"),
		})
		if err != nil {
			return err
		}

		var publicSubnets []*ec2.Subnet
		var privateSubnets []*ec2.Subnet

		igw, err := ec2.NewInternetGateway(ctx, "kafka-playground-igw", &ec2.InternetGatewayArgs{
			VpcId: vpc.ID(),
			Tags: pulumi.StringMap{
				"Name": pulumi.String("kafka-playground-igw"),
			},
		})
		if err != nil {
			return err
		}

		publicRouteTable, err := ec2.NewRouteTable(ctx, "kafka-playground-public-route-table", &ec2.RouteTableArgs{
			VpcId: vpc.ID(),
			Routes: ec2.RouteTableRouteArray{
				&ec2.RouteTableRouteArgs{
					CidrBlock: pulumi.String("0.0.0.0/0"),
					GatewayId: igw.ID(),
				},
			},
			Tags: pulumi.StringMap{
				"Name": pulumi.String("kafka-playground-public-route-table"),
			},
		})
		if err != nil {
			return err
		}

		for i := 0; i < 3; i++ {
			azName := availableAzs.Names[i]
			publicSubnet, err := ec2.NewSubnet(ctx, fmt.Sprintf("kafka-playground-public-subnet-%s", azName), &ec2.SubnetArgs{
				VpcId:               vpc.ID(),
				CidrBlock:           pulumi.String(fmt.Sprintf("10.0.%d.0/24", i*2)),
				AvailabilityZone:    pulumi.String(azName),
				MapPublicIpOnLaunch: pulumi.Bool(true),
				Tags: pulumi.StringMap{
					"Name": pulumi.String(fmt.Sprintf("kafka-playground-public-%s", azName)),
				},
			})
			if err != nil {
				return err
			}
			publicSubnets = append(publicSubnets, publicSubnet)

			_, err = ec2.NewRouteTableAssociation(ctx, fmt.Sprintf("kafka-playground-public-rta-%s", azName), &ec2.RouteTableAssociationArgs{
				SubnetId:     publicSubnet.ID(),
				RouteTableId: publicRouteTable.ID(),
			})
			if err != nil {
				return err
			}

			privateSubnet, err := ec2.NewSubnet(ctx, fmt.Sprintf("kafka-playground-private-subnet-%s", azName), &ec2.SubnetArgs{
				VpcId:            vpc.ID(),
				CidrBlock:        pulumi.String(fmt.Sprintf("10.0.%d.0/24", i*2+1)),
				AvailabilityZone: pulumi.String(azName),
				Tags: pulumi.StringMap{
					"Name": pulumi.String(fmt.Sprintf("kafka-playground-private-%s", azName)),
				},
			})
			if err != nil {
				return err
			}
			privateSubnets = append(privateSubnets, privateSubnet)
		}

		eip, err := ec2.NewEip(ctx, "kafka-playground-nat-eip", &ec2.EipArgs{
			Domain: pulumi.String("vpc"),
		})
		if err != nil {
			return err
		}

		natGw, err := ec2.NewNatGateway(ctx, "kafka-playground-nat-gw", &ec2.NatGatewayArgs{
			AllocationId: eip.ID(),
			SubnetId:     publicSubnets[0].ID(),
			Tags: pulumi.StringMap{
				"Name": pulumi.String("kafka-playground-nat-gw"),
			},
		})
		if err != nil {
			return err
		}

		for i, privateSubnet := range privateSubnets {
			privateRouteTable, err := ec2.NewRouteTable(ctx, fmt.Sprintf("kafka-playground-private-rt-%d", i), &ec2.RouteTableArgs{
				VpcId: vpc.ID(),
				Routes: ec2.RouteTableRouteArray{
					&ec2.RouteTableRouteArgs{
						CidrBlock:    pulumi.String("0.0.0.0/0"),
						NatGatewayId: natGw.ID(),
					},
				},
				Tags: pulumi.StringMap{
					"Name": pulumi.String(fmt.Sprintf("kafka-playground-private-rt-%d", i)),
				},
			})
			if err != nil {
				return err
			}
			_, err = ec2.NewRouteTableAssociation(ctx, fmt.Sprintf("kafka-playground-private-rta-%d", i), &ec2.RouteTableAssociationArgs{
				SubnetId:     privateSubnet.ID(),
				RouteTableId: privateRouteTable.ID(),
			})
			if err != nil {
				return err
			}
		}

		ami, err := ec2.LookupAmi(ctx, &ec2.LookupAmiArgs{
			MostRecent: pulumi.BoolRef(true),
			Owners:     []string{"amazon"},
			Filters: []ec2.GetAmiFilter{
				{
					Name:   "name",
					Values: []string{"amzn2-ami-hvm-*-x86_64-gp2"},
				},
			},
		}, nil)
		if err != nil {
			return err
		}

		kafkaSg, err := ec2.NewSecurityGroup(ctx, "kafka-playground-sg", &ec2.SecurityGroupArgs{
			VpcId:       vpc.ID(),
			Description: pulumi.String("Allow Kafka traffic"),
			Tags: pulumi.StringMap{
				"Name": pulumi.String("kafka-playground-sg"),
			},
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewSecurityGroupRule(ctx, "kafka-ingress-clients", &ec2.SecurityGroupRuleArgs{
			Type:            pulumi.String("ingress"),
			FromPort:        pulumi.Int(9092),
			ToPort:          pulumi.Int(9092),
			Protocol:        pulumi.String("tcp"),
			CidrBlocks:      pulumi.StringArray{vpc.CidrBlock},
			SecurityGroupId: kafkaSg.ID(),
			Description:     pulumi.String("Allow Kafka clients from within the VPC"),
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewSecurityGroupRule(ctx, "kafka-ingress-kraft", &ec2.SecurityGroupRuleArgs{
			Type:                  pulumi.String("ingress"),
			FromPort:              pulumi.Int(9093),
			ToPort:                pulumi.Int(9093),
			Protocol:              pulumi.String("tcp"),
			SourceSecurityGroupId: kafkaSg.ID(),
			SecurityGroupId:       kafkaSg.ID(),
			Description:           pulumi.String("Allow internal KRaft communication"),
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewSecurityGroupRule(ctx, "kafka-egress-all", &ec2.SecurityGroupRuleArgs{
			Type:            pulumi.String("egress"),
			FromPort:        pulumi.Int(0),
			ToPort:          pulumi.Int(0),
			Protocol:        pulumi.String("-1"),
			CidrBlocks:      pulumi.StringArray{pulumi.String("0.0.0.0/0")},
			SecurityGroupId: kafkaSg.ID(),
			Description:     pulumi.String("Allow all outbound traffic"),
		})
		if err != nil {
			return err
		}

		hostedZone, err := route53.NewZone(ctx, "kafka-playground-zone", &route53.ZoneArgs{
			Name: pulumi.String("kafka.internal"),
			Vpcs: route53.ZoneVpcArray{
				&route53.ZoneVpcArgs{
					VpcId: vpc.ID(),
				},
			},
			Tags: pulumi.StringMap{
				"Name": pulumi.String("kafka-playground-zone"),
			},
		})
		if err != nil {
			return err
		}

		var brokerEndpoints pulumi.StringArray
		var controllerVoters []string
		for i := 0; i < 7; i++ {
			instanceName := fmt.Sprintf("kafka-playground-instance-%d", i)
			domainName := fmt.Sprintf("%s.kafka.internal", instanceName)
			controllerVoters = append(controllerVoters, fmt.Sprintf("%d@%s:9093", i, domainName))
			brokerEndpoints = append(brokerEndpoints, pulumi.Sprintf("%s:9092", domainName))
		}
		controllerVotersString := strings.Join(controllerVoters, ",")

		for i := 0; i < 7; i++ {
			instance, err := ec2.NewInstance(ctx, fmt.Sprintf("kafka-playground-instance-%d", i), &ec2.InstanceArgs{
				InstanceType: pulumi.String("t2.micro"),
				Ami:          pulumi.String(ami.Id),
				SubnetId:     privateSubnets[i%len(privateSubnets)].ID(),
				VpcSecurityGroupIds: pulumi.StringArray{
					kafkaSg.ID(),
				},
				UserData: pulumi.String(fmt.Sprintf(`#!/bin/bash
sudo yum update -y
sudo yum install -y java-11-amazon-corretto
wget https://downloads.apache.org/kafka/3.9.1/kafka_2.13-3.9.1.tgz
tar -xzf kafka_2.13-3.9.1.tgz
cd kafka_2.13-3.9.1
CLUSTER_ID=$(bin/kafka-storage.sh random-uuid)
bin/kafka-storage.sh format -t $CLUSTER_ID -c config/kraft/server.properties
sed -i 's/broker.id=0/broker.id=%d/' config/kraft/server.properties
sed -i 's|controller.quorum.voters=.*|controller.quorum.voters=%s|' config/kraft/server.properties
bin/kafka-server-start.sh -daemon config/kraft/server.properties
`, i, controllerVotersString)),
				Tags: pulumi.StringMap{
					"Name": pulumi.String(fmt.Sprintf("kafka-playground-instance-%d", i)),
				},
			})
			if err != nil {
				return err
			}

			_, err = route53.NewRecord(ctx, fmt.Sprintf("kafka-playground-record-%d", i), &route53.RecordArgs{
				ZoneId: hostedZone.ID(),
				Name: instance.Tags.ApplyT(func(tags map[string]string) (string, error) {
					return fmt.Sprintf("%s.kafka.internal", tags["Name"]), nil
				}).(pulumi.StringOutput),
				Type: pulumi.String("A"),
				Ttl:  pulumi.Int(300),
				Records: pulumi.StringArray{
					instance.PrivateIp,
				},
			})
			if err != nil {
				return err
			}
		}

		ctx.Export("broker_endpoints", brokerEndpoints)

		return nil
	})
}
