#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/in.h>
#include <linux/ip.h>
#include <linux/tcp.h>
#include <linux/udp.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

// Flow event structure that matches our protobuf definition
struct flow_event {
    __u32 src_ip;
    __u32 dst_ip;
    __u16 src_port;
    __u16 dst_port;
    __u8 protocol;
    __u8 verdict;
    __u64 timestamp;
};

// BPF map to send events to userspace
struct {
    __uint(type, BPF_MAP_TYPE_PERF_EVENT_ARRAY);
    __uint(key_size, sizeof(int));
    __uint(value_size, sizeof(int));
    __uint(max_entries, 1024);
} events SEC(".maps");

SEC("classifier")
int tc_flow_observer(struct __sk_buff *skb) {
    // Verify it's an IP packet
    if (skb->protocol != bpf_htons(ETH_P_IP))
        return TC_ACT_OK;

    struct flow_event event = {};
    
    // Get IP header
    struct iphdr iph;
    if (bpf_skb_load_bytes(skb, ETH_HLEN, &iph, sizeof(iph)) < 0)
        return TC_ACT_OK;

    // Store L3 info
    event.src_ip = iph.saddr;
    event.dst_ip = iph.daddr;
    event.protocol = iph.protocol;
    
    // Get L4 info based on protocol
    if (iph.protocol == IPPROTO_TCP) {
        struct tcphdr tcp;
        if (bpf_skb_load_bytes(skb, ETH_HLEN + sizeof(iph), &tcp, sizeof(tcp)) < 0)
            return TC_ACT_OK;
        event.src_port = bpf_ntohs(tcp.source);
        event.dst_port = bpf_ntohs(tcp.dest);
    } else if (iph.protocol == IPPROTO_UDP) {
        struct udphdr udp;
        if (bpf_skb_load_bytes(skb, ETH_HLEN + sizeof(iph), &udp, sizeof(udp)) < 0)
            return TC_ACT_OK;
        event.src_port = bpf_ntohs(udp.source);
        event.dst_port = bpf_ntohs(udp.dest);
    }

    // Set timestamp and verdict
    event.timestamp = bpf_ktime_get_ns();
    event.verdict = TC_ACT_OK;  // We'll set FORWARDED/DROPPED in userspace

    // Send event to userspace
    bpf_perf_event_output(skb, &events, BPF_F_CURRENT_CPU, &event, sizeof(event));

    return TC_ACT_OK;
}

char LICENSE[] SEC("license") = "GPL"; 