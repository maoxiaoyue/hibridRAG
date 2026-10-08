// cmd/inbound-test — 對本機 gRPC AgentService:9091 送一筆 InboundReq，
// 驗證完整鏈路：gRPC → SkillPack → ContextBuilder → LLM(local) → Episodic write → log。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"agent/app/models"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9091", "agent gRPC endpoint")
	agentID := flag.String("agent", "demo", "target agent_id (matches skills/<id>/SKILL.md)")
	from := flag.String("from", "upstream-test", "caller id (OutboundMsg.From after reply)")
	msg := flag.String("msg", "用一句話自我介紹", "user message")
	traceID := flag.String("trace", fmt.Sprintf("tt_%d", time.Now().Unix()), "trace_id")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	client := models.NewAgentServiceClient(conn)

	req := &models.InboundReq{
		TraceId: *traceID,
		From:    *from,
		Payload: &models.AgentReq{
			SessionId: "sess_" + *traceID,
			UserId:    "u_test",
			AgentId:   *agentID,
			Messages: []*models.Message{
				{Role: "user", Content: *msg},
			},
		},
	}

	fmt.Printf("→ Inbound: trace=%s agent=%s from=%s msg=%q\n", *traceID, *agentID, *from, *msg)
	start := time.Now()
	ack, err := client.Inbound(ctx, req)
	elapsed := time.Since(start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Inbound RPC error after %s: %v\n", elapsed, err)
		os.Exit(1)
	}

	fmt.Printf("← Ack (%s): accepted=%v code=%d msg_id=%s trace=%s err=%q\n",
		elapsed, ack.Accepted, ack.Code, ack.MessageId, ack.TraceId, ack.Error)
}
