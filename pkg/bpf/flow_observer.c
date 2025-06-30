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

// Flow event structure for perf buffer
struct flow_event {
    __u32 src_ip;
    __u32 dst_ip;
    __u16 src_port;
    __u16 dst_port;
    __u8 protocol;
    __u8 verdict;
    __u16 _pad;  // padding to align timestamp
    __u64 timestamp;
} __attribute__((packed));

// BPF map to store flow information
struct {
    __uint(type, BPF_MAP_TYPE_PERF_EVENT_ARRAY);
    __uint(key_size, sizeof(__u32));
    __uint(value_size, sizeof(__u32));
    __uint(max_entries, 10240);
} flow_events SEC(".maps");

// Helper function to extract ports
static __always_inline void extract_ports(void *transport_header, void *data_end, __u8 protocol,
                                        struct flow_event *flow) {
    switch (protocol) {
        case IPPROTO_TCP: {
            struct tcphdr *tcp = transport_header;
            if ((void*)(tcp + 1) > data_end)
                return;
            flow->src_port = bpf_ntohs(tcp->source);
            flow->dst_port = bpf_ntohs(tcp->dest);
            break;
        }
        case IPPROTO_UDP: {
            struct udphdr *udp = transport_header;
            if ((void*)(udp + 1) > data_end)
                return;
            flow->src_port = bpf_ntohs(udp->source);
            flow->dst_port = bpf_ntohs(udp->dest);
            break;
        }
        default:
            flow->src_port = 0;
            flow->dst_port = 0;
    }
}

SEC("tc")
int flow_observer(struct __sk_buff *skb) {
    // Initialize flow event
    struct flow_event flow = {};
    
    // Get IP header
    void *data = (void *)(long)skb->data;
    void *data_end = (void *)(long)skb->data_end;
    
    struct ethhdr *eth = data;
    if ((void*)(eth + 1) > data_end)
        return TC_ACT_OK;
        
    if (eth->h_proto != bpf_htons(ETH_P_IP))
        return TC_ACT_OK;
        
    struct iphdr *ip = (void*)(eth + 1);
    if ((void*)(ip + 1) > data_end)
        return TC_ACT_OK;

    // Store IP info
    flow.src_ip = ip->saddr;
    flow.dst_ip = ip->daddr;
    flow.protocol = ip->protocol;

    // Get transport header
    void *transport_header = (void*)(ip + 1);
    extract_ports(transport_header, data_end, ip->protocol, &flow);

    // Set metadata
    flow.verdict = TC_ACT_OK;  // Default to FORWARD
    flow._pad = 0;  // Clear padding
    flow.timestamp = bpf_ktime_get_ns();

    // Validate flow data before submission
    if (flow.src_ip == 0 || flow.dst_ip == 0) {
        return TC_ACT_OK;  // Skip invalid flows
    }

    // Ensure protocol is valid
    if (flow.protocol != IPPROTO_TCP && flow.protocol != IPPROTO_UDP) {
        return TC_ACT_OK;  // Skip non-TCP/UDP for now
    }

    // Send event to userspace
    int ret = bpf_perf_event_output(skb, &flow_events, BPF_F_CURRENT_CPU,
                                   &flow, sizeof(flow));
    if (ret < 0) {
        // Just log the error by incrementing a counter in a future version
    }

    return TC_ACT_OK;
}

char LICENSE[] SEC("license") = "GPL"; 