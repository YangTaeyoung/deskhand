package cli

import (
	"context"
	"fmt"

	"github.com/YangTaeyoung/deskhand/internal/mcpserver"
)

// mcp serves the MCP tools over stdin/stdout until the MCP client
// disconnects.
func (r *run) mcp(ctx context.Context, args []string) int {
	if len(args) > 0 {
		return r.fail(usagef("deskhand mcp는 옵션이 없습니다. MCP 클라이언트 설정에 명령 `deskhand mcp`만 등록하세요"))
	}
	if err := mcpserver.Run(ctx, r.Version); err != nil && ctx.Err() == nil {
		fmt.Fprintln(r.Stderr, "오류: MCP 연결이 끝났습니다. MCP 클라이언트에서 서버를 다시 시작하세요: "+err.Error())
		return exitFailure
	}
	return exitOK
}
