# Kafka Playground on AWS

This project uses Pulumi to deploy a production-ready, highly available Apache Kafka cluster on AWS.

## Architecture Diagram

The following diagram illustrates the architecture of the deployed Kafka cluster.

```mermaid
graph TD
    subgraph "AWS Cloud"
        subgraph VPC ["VPC (10.0.0.0/16)"]
            
            subgraph AZ1 ["Availability Zone A"]
                subgraph PubSub1 [Public Subnet]
                    NAT(NAT Gateway)
                end
                subgraph PrivSub1 [Private Subnet]
                    K1("Kafka Broker(s)")
                end
            end
            
            subgraph AZ2 ["Availability Zone B"]
                subgraph PubSub2 [Public Subnet]
                    p2(" ")
                end
                subgraph PrivSub2 [Private Subnet]
                    K2("Kafka Broker(s)")
                end
            end
            
            subgraph AZ3 ["Availability Zone C"]
                 subgraph PubSub3 [Public Subnet]
                    p3(" ")
                end
                subgraph PrivSub3 [Private Subnet]
                    K3("Kafka Broker(s)")
                end
            end
            
            Client(VPC Client) --> K1
            Client --> K2
            Client --> K3
            
            Internet[/"Internet"/] <--> IGW(Internet<br>Gateway)
            IGW --> PubSub1
            IGW --> PubSub2
            IGW --> PubSub3
            
            PrivSub1 --> NAT
            PrivSub2 --> NAT
            PrivSub3 --> NAT
            NAT --> EIP(Elastic IP)
            EIP --> IGW
            
            R53(Route 53<br>Private Zone<br>kafka.internal)
            R53 -- "Resolves<br>*.kafka.internal" --> K1
            R53 -- "Resolves<br>*.kafka.internal" --> K2
            R53 -- "Resolves<br>*.kafka.internal" --> K3
            
            K1 <-- "KRaft &<br>Replication" --> K2
            K2 <-- "KRaft &<br>Replication" --> K3
            K3 <-- "KRaft &<br>Replication" --> K1
        end
    end
style K1 fill:#f9f,stroke:#333,stroke-width:2px
style K2 fill:#f9f,stroke:#333,stroke-width:2px
style K3 fill:#f9f,stroke:#333,stroke-width:2px
style p2 fill:#fff,stroke:#fff
style p3 fill:#fff,stroke:#fff
``` 