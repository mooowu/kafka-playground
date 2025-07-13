package main

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/route53"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "")
		keyName := cfg.Require("keyName")

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

		bastionSg, err := ec2.NewSecurityGroup(ctx, "kafka-playground-bastion-sg", &ec2.SecurityGroupArgs{
			VpcId:       vpc.ID(),
			Description: pulumi.String("Allow SSH to bastion host"),
			Tags: pulumi.StringMap{
				"Name": pulumi.String("kafka-playground-bastion-sg"),
			},
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewSecurityGroupRule(ctx, "bastion-ingress-ssh", &ec2.SecurityGroupRuleArgs{
			Type:            pulumi.String("ingress"),
			FromPort:        pulumi.Int(22),
			ToPort:          pulumi.Int(22),
			Protocol:        pulumi.String("tcp"),
			CidrBlocks:      pulumi.StringArray{pulumi.String("0.0.0.0/0")},
			SecurityGroupId: bastionSg.ID(),
			Description:     pulumi.String("Allow SSH from Internet"),
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewSecurityGroupRule(ctx, "bastion-egress-all", &ec2.SecurityGroupRuleArgs{
			Type:     pulumi.String("egress"),
			FromPort: pulumi.Int(0),
			ToPort:   pulumi.Int(0),
			Protocol: pulumi.String("-1"),
			CidrBlocks: pulumi.StringArray{
				pulumi.String("0.0.0.0/0"),
			},
			SecurityGroupId: bastionSg.ID(),
			Description:     pulumi.String("Allow all outbound traffic from bastion"),
		})
		if err != nil {
			return err
		}

		controllerSg, err := ec2.NewSecurityGroup(ctx, "kafka-playground-controller-sg", &ec2.SecurityGroupArgs{
			VpcId:       vpc.ID(),
			Description: pulumi.String("Allow Kafka controller traffic"),
			Tags: pulumi.StringMap{
				"Name": pulumi.String("kafka-playground-controller-sg"),
			},
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewSecurityGroupRule(ctx, "controller-ingress-kraft", &ec2.SecurityGroupRuleArgs{
			Type:                  pulumi.String("ingress"),
			FromPort:              pulumi.Int(9093),
			ToPort:                pulumi.Int(9093),
			Protocol:              pulumi.String("tcp"),
			SourceSecurityGroupId: controllerSg.ID(),
			SecurityGroupId:       controllerSg.ID(),
			Description:           pulumi.String("Allow internal KRaft communication"),
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewSecurityGroupRule(ctx, "controller-ingress-ssh-from-bastion", &ec2.SecurityGroupRuleArgs{
			Type:                  pulumi.String("ingress"),
			FromPort:              pulumi.Int(22),
			ToPort:                pulumi.Int(22),
			Protocol:              pulumi.String("tcp"),
			SourceSecurityGroupId: bastionSg.ID(),
			SecurityGroupId:       controllerSg.ID(),
			Description:           pulumi.String("Allow SSH from bastion to Kafka controllers"),
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewSecurityGroupRule(ctx, "controller-egress-all", &ec2.SecurityGroupRuleArgs{
			Type:     pulumi.String("egress"),
			FromPort: pulumi.Int(0),
			ToPort:   pulumi.Int(0),
			Protocol: pulumi.String("-1"),
			CidrBlocks: pulumi.StringArray{
				pulumi.String("0.0.0.0/0"),
			},
			SecurityGroupId: controllerSg.ID(),
			Description:     pulumi.String("Allow all outbound traffic"),
		})
		if err != nil {
			return err
		}

		brokerSg, err := ec2.NewSecurityGroup(ctx, "kafka-playground-broker-sg", &ec2.SecurityGroupArgs{
			VpcId:       vpc.ID(),
			Description: pulumi.String("Allow Kafka broker traffic"),
			Tags: pulumi.StringMap{
				"Name": pulumi.String("kafka-playground-broker-sg"),
			},
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewSecurityGroupRule(ctx, "broker-ingress-clients", &ec2.SecurityGroupRuleArgs{
			Type:            pulumi.String("ingress"),
			FromPort:        pulumi.Int(9092),
			ToPort:          pulumi.Int(9092),
			Protocol:        pulumi.String("tcp"),
			CidrBlocks:      pulumi.StringArray{vpc.CidrBlock},
			SecurityGroupId: brokerSg.ID(),
			Description:     pulumi.String("Allow Kafka clients from within the VPC"),
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewSecurityGroupRule(ctx, "broker-ingress-ssh-from-bastion", &ec2.SecurityGroupRuleArgs{
			Type:                  pulumi.String("ingress"),
			FromPort:              pulumi.Int(22),
			ToPort:                pulumi.Int(22),
			Protocol:              pulumi.String("tcp"),
			SourceSecurityGroupId: bastionSg.ID(),
			SecurityGroupId:       brokerSg.ID(),
			Description:           pulumi.String("Allow SSH from bastion to Kafka brokers"),
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewSecurityGroupRule(ctx, "broker-egress-all", &ec2.SecurityGroupRuleArgs{
			Type:     pulumi.String("egress"),
			FromPort: pulumi.Int(0),
			ToPort:   pulumi.Int(0),
			Protocol: pulumi.String("-1"),
			CidrBlocks: pulumi.StringArray{
				pulumi.String("0.0.0.0/0"),
			},
			SecurityGroupId: brokerSg.ID(),
			Description:     pulumi.String("Allow all outbound traffic"),
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewSecurityGroupRule(ctx, "controller-ingress-from-brokers", &ec2.SecurityGroupRuleArgs{
			Type:                  pulumi.String("ingress"),
			FromPort:              pulumi.Int(9093),
			ToPort:                pulumi.Int(9093),
			Protocol:              pulumi.String("tcp"),
			SourceSecurityGroupId: brokerSg.ID(),
			SecurityGroupId:       controllerSg.ID(),
			Description:           pulumi.String("Allow brokers to connect to controllers"),
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

		var controllerVoters []string
		for i := 0; i < 3; i++ {
			controllerName := fmt.Sprintf("kafka-playground-controller-%d", i)
			controllerDomain := fmt.Sprintf("%s.kafka.internal", controllerName)
			controllerVoters = append(controllerVoters, fmt.Sprintf("%d@%s:9093", i, controllerDomain))
		}
		controllerVotersString := strings.Join(controllerVoters, ",")

		for i := 0; i < 3; i++ {
			nodeID := i
			instanceName := fmt.Sprintf("kafka-playground-controller-%d", i)
			instance, err := ec2.NewInstance(ctx, instanceName, &ec2.InstanceArgs{
				InstanceType: pulumi.String("t3.small"),
				Ami:          pulumi.String(ami.Id),
				SubnetId:     privateSubnets[i%len(privateSubnets)].ID(),
				VpcSecurityGroupIds: pulumi.StringArray{
					controllerSg.ID(),
				},
				KeyName: pulumi.String(keyName),
				UserData: pulumi.String(fmt.Sprintf(`#!/bin/bash
sudo yum update -y
sudo yum install -y java-11-amazon-corretto
sudo useradd -r -m -s /bin/bash kafka
sudo wget https://downloads.apache.org/kafka/3.9.1/kafka_2.13-3.9.1.tgz -O /tmp/kafka_2.13-3.9.1.tgz
sudo tar -xzf /tmp/kafka_2.13-3.9.1.tgz -C /opt/
sudo mv /opt/kafka_2.13-3.9.1 /opt/kafka
sudo chown -R kafka:kafka /opt/kafka
sudo mkdir -p /var/log/kafka
sudo mkdir -p /opt/kafka/logs
sudo chown -R kafka:kafka /var/log/kafka
sudo chown -R kafka:kafka /opt/kafka/logs
CLUSTER_ID="kafka-playground-cluster-id-12345"
sudo cp /opt/kafka/config/kraft/controller.properties /opt/kafka/config/kraft/controller.properties.backup
sudo bash -c "cat > /opt/kafka/config/kraft/controller.properties << EOF
process.roles=controller
node.id=%d
controller.quorum.voters=%s
listeners=CONTROLLER://:9093
controller.listener.names=CONTROLLER
listener.security.protocol.map=CONTROLLER:PLAINTEXT
log.dirs=/opt/kafka/logs
num.network.threads=3
num.io.threads=8
socket.send.buffer.bytes=102400
socket.receive.buffer.bytes=102400
socket.request.max.bytes=104857600
EOF"
sudo chown kafka:kafka /opt/kafka/config/kraft/controller.properties
sudo -u kafka /opt/kafka/bin/kafka-storage.sh format -t $CLUSTER_ID -c /opt/kafka/config/kraft/controller.properties --ignore-formatted
sudo cat > /etc/systemd/system/kafka.service << 'EOF'
[Unit]
Description=Apache Kafka Controller Service
After=network.target
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User=kafka
Group=kafka
ExecStart=/opt/kafka/bin/kafka-server-start.sh /opt/kafka/config/kraft/controller.properties
ExecStop=/opt/kafka/bin/kafka-server-stop.sh
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=kafka-controller
KillMode=process
TimeoutStopSec=30
[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable kafka
sudo systemctl start kafka
sleep 30
sudo systemctl status kafka
`, nodeID, controllerVotersString)),
				Tags: pulumi.StringMap{
					"Name": pulumi.String(instanceName),
				},
			})
			if err != nil {
				return err
			}

			_, err = route53.NewRecord(ctx, fmt.Sprintf("kafka-playground-controller-record-%d", i), &route53.RecordArgs{
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

		var brokerEndpoints pulumi.StringArray
		for i := 0; i < 3; i++ {
			nodeID := i + 1000
			instanceName := fmt.Sprintf("kafka-playground-broker-%d", i)
			advertisedListener := fmt.Sprintf("%s.kafka.internal:9092", instanceName)
			brokerEndpoints = append(brokerEndpoints, pulumi.String(advertisedListener))

			instance, err := ec2.NewInstance(ctx, instanceName, &ec2.InstanceArgs{
				InstanceType: pulumi.String("t3.medium"),
				Ami:          pulumi.String(ami.Id),
				SubnetId:     privateSubnets[i%len(privateSubnets)].ID(),
				VpcSecurityGroupIds: pulumi.StringArray{
					brokerSg.ID(),
				},
				KeyName: pulumi.String(keyName),
				UserData: pulumi.String(fmt.Sprintf(`#!/bin/bash
sudo yum update -y
sudo yum install -y java-11-amazon-corretto
sudo useradd -r -m -s /bin/bash kafka
sudo wget https://downloads.apache.org/kafka/3.9.1/kafka_2.13-3.9.1.tgz -O /tmp/kafka_2.13-3.9.1.tgz
sudo tar -xzf /tmp/kafka_2.13-3.9.1.tgz -C /opt/
sudo mv /opt/kafka_2.13-3.9.1 /opt/kafka
sudo chown -R kafka:kafka /opt/kafka
sudo mkdir -p /var/log/kafka
sudo mkdir -p /opt/kafka/logs
sudo chown -R kafka:kafka /var/log/kafka
sudo chown -R kafka:kafka /opt/kafka/logs
CLUSTER_ID="kafka-playground-cluster-id-12345"
sudo cp /opt/kafka/config/kraft/broker.properties /opt/kafka/config/kraft/broker.properties.backup
sudo bash -c "cat > /opt/kafka/config/kraft/broker.properties << EOF
process.roles=broker
node.id=%d
controller.quorum.voters=%s
listeners=PLAINTEXT://:9092
advertised.listeners=PLAINTEXT://%s
controller.listener.names=CONTROLLER
listener.security.protocol.map=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT
num.network.threads=3
num.io.threads=8
socket.send.buffer.bytes=102400
socket.receive.buffer.bytes=102400
socket.request.max.bytes=104857600
log.dirs=/opt/kafka/logs
num.partitions=3
num.recovery.threads.per.data.dir=1
offsets.topic.replication.factor=3
transaction.state.log.replication.factor=3
transaction.state.log.min.isr=2
log.retention.hours=168
log.segment.bytes=1073741824
log.retention.check.interval.ms=300000
auto.create.topics.enable=true
group.initial.rebalance.delay.ms=0
EOF"
sudo chown kafka:kafka /opt/kafka/config/kraft/broker.properties
sudo -u kafka /opt/kafka/bin/kafka-storage.sh format -t $CLUSTER_ID -c /opt/kafka/config/kraft/broker.properties --ignore-formatted
sudo cat > /etc/systemd/system/kafka.service << 'EOF'
[Unit]
Description=Apache Kafka Broker Service
After=network.target
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User=kafka
Group=kafka
ExecStart=/opt/kafka/bin/kafka-server-start.sh /opt/kafka/config/kraft/broker.properties
ExecStop=/opt/kafka/bin/kafka-server-stop.sh
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=kafka-broker
KillMode=process
TimeoutStopSec=30
[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable kafka
sudo systemctl start kafka
sleep 30
sudo systemctl status kafka
`, nodeID, controllerVotersString, advertisedListener)),
				Tags: pulumi.StringMap{
					"Name": pulumi.String(instanceName),
				},
			})
			if err != nil {
				return err
			}

			_, err = route53.NewRecord(ctx, fmt.Sprintf("kafka-playground-broker-record-%d", i), &route53.RecordArgs{
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

		bastion, err := ec2.NewInstance(ctx, "kafka-playground-bastion", &ec2.InstanceArgs{
			InstanceType: pulumi.String("t2.micro"),
			Ami:          pulumi.String(ami.Id),
			SubnetId:     publicSubnets[0].ID(),
			VpcSecurityGroupIds: pulumi.StringArray{
				bastionSg.ID(),
			},
			KeyName: pulumi.String(keyName),
			UserData: pulumi.String(`#!/bin/bash
sudo yum update -y
sudo yum install -y java-11-amazon-corretto
wget https://downloads.apache.org/kafka/3.9.1/kafka_2.13-3.9.1.tgz
tar -xzf kafka_2.13-3.9.1.tgz
`),
			Tags: pulumi.StringMap{
				"Name": pulumi.String("kafka-playground-bastion"),
			},
		})
		if err != nil {
			return err
		}

		ctx.Export("broker_endpoints", brokerEndpoints)
		ctx.Export("bastion_public_ip", bastion.PublicIp)

		return nil
	})
}
