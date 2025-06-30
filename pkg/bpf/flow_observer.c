#include <linux/types.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <linux/tcp.h>
#include <linux/udp.h>
#include <linux/in.h>
#include <linux/pkt_cls.h>  // For TC_ACT_* definitions

// VXLAN header structure
struct vxlan_header {
    __u32 vx_flags;
    __u32 vx_vni;
};

// Flow event structure for perf buffer
struct flow_event {
    __u64 timestamp;
    __u32 src_ip;
    __u32 dst_ip;
    __u16 src_port;
    __u16 dst_port;
    __u8 protocol;
    __u8 verdict;
} __attribute__((packed));

// BPF map to store flow information
struct {
    __uint(type, BPF_MAP_TYPE_PERF_EVENT_ARRAY);
    __uint(key_size, sizeof(__u32));
    __uint(value_size, sizeof(__u32));
    __uint(max_entries, 10240);
} flow_events SEC(".maps");

// Helper function to extract ports
static __always_inline void extract_ports(struct __sk_buff *skb, __u8 protocol, __u16 *src_port, __u16 *dst_port, __u32 ip_payload_offset) {
    switch (protocol) {
        case IPPROTO_TCP: {
            struct tcphdr tcp;
            if (bpf_skb_load_bytes(skb, ip_payload_offset, &tcp, sizeof(tcp)) < 0)
                return;
            *src_port = bpf_ntohs(tcp.source);
            *dst_port = bpf_ntohs(tcp.dest);
            break;
        }
        case IPPROTO_UDP: {
            struct udphdr udp;
            if (bpf_skb_load_bytes(skb, ip_payload_offset, &udp, sizeof(udp)) < 0)
                return;
            *src_port = bpf_ntohs(udp.source);
            *dst_port = bpf_ntohs(udp.dest);
            break;
        }
        default:
            *src_port = 0;
            *dst_port = 0;
    }
}

// Helper function to process IP packet
static __always_inline void process_ip_packet(struct __sk_buff *skb, __u32 ip_offset) {
    struct iphdr iph;
    if (bpf_skb_load_bytes(skb, ip_offset, &iph, sizeof(iph)) < 0)
        return;

    // Check if this is VXLAN traffic (UDP port 4789)
    if (iph.protocol == IPPROTO_UDP) {
        struct udphdr udp;
        if (bpf_skb_load_bytes(skb, ip_offset + sizeof(iph), &udp, sizeof(udp)) < 0)
            return;

        // If this is VXLAN traffic
        if (bpf_ntohs(udp.dest) == 4789) {
            // Skip VXLAN header to get to inner Ethernet frame
            __u32 inner_eth_offset = ip_offset + sizeof(iph) + sizeof(udp) + sizeof(struct vxlan_header);
            struct ethhdr inner_eth;
            if (bpf_skb_load_bytes(skb, inner_eth_offset, &inner_eth, sizeof(inner_eth)) < 0)
                return;

            // Process inner IP packet if it's IPv4
            if (bpf_ntohs(inner_eth.h_proto) == ETH_P_IP) {
                struct iphdr inner_ip;
                __u32 inner_ip_offset = inner_eth_offset + sizeof(inner_eth);
                if (bpf_skb_load_bytes(skb, inner_ip_offset, &inner_ip, sizeof(inner_ip)) < 0)
                    return;

                // Extract ports from inner packet
                __u16 src_port = 0, dst_port = 0;
                extract_ports(skb, inner_ip.protocol, &src_port, &dst_port, inner_ip_offset + sizeof(inner_ip));

                // Create flow event with inner packet information
                struct flow_event event = {
                    .timestamp = bpf_ktime_get_ns(),
                    .src_ip = inner_ip.saddr,
                    .dst_ip = inner_ip.daddr,
                    .src_port = src_port,
                    .dst_port = dst_port,
                    .protocol = inner_ip.protocol,
                    .verdict = TC_ACT_OK
                };

                bpf_perf_event_output(skb, &flow_events, BPF_F_CURRENT_CPU, &event, sizeof(event));
            }
            return;
        }
    }

    // For non-VXLAN traffic, process normally
    __u16 src_port = 0, dst_port = 0;
    extract_ports(skb, iph.protocol, &src_port, &dst_port, ip_offset + sizeof(iph));

    struct flow_event event = {
        .timestamp = bpf_ktime_get_ns(),
        .src_ip = iph.saddr,
        .dst_ip = iph.daddr,
        .src_port = src_port,
        .dst_port = dst_port,
        .protocol = iph.protocol,
        .verdict = TC_ACT_OK
    };

    bpf_perf_event_output(skb, &flow_events, BPF_F_CURRENT_CPU, &event, sizeof(event));
}

SEC("classifier")
int flow_observer(struct __sk_buff *skb) {
    // Load ethernet header
    struct ethhdr eth;
    if (bpf_skb_load_bytes(skb, 0, &eth, sizeof(eth)) < 0)
        return TC_ACT_OK;

    // Only process IPv4 packets
    if (bpf_ntohs(eth.h_proto) == ETH_P_IP) {
        process_ip_packet(skb, sizeof(eth));
    }

    return TC_ACT_OK;
}

char LICENSE[] SEC("license") = "GPL"; 