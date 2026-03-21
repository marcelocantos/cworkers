// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0
// Embed help-agent.md at link time via .incbin.

#ifdef __APPLE__
.section __DATA,__const
.globl _help_agent_data
.globl _help_agent_size
_help_agent_data:
    .incbin "help-agent.md"
_help_agent_end:
.p2align 3
_help_agent_size:
    .quad _help_agent_end - _help_agent_data
#else
.section .rodata
.globl help_agent_data
.globl help_agent_size
help_agent_data:
    .incbin "help-agent.md"
help_agent_end:
.p2align 3
help_agent_size:
    .quad help_agent_end - help_agent_data
#endif
