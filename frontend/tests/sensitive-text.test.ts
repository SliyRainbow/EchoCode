import { describe, expect, test } from "bun:test";

import { redactSensitiveTextForDisplay } from "../src/lib/sensitive-text";

describe("sensitive chat text", () => {
  test("hides SSH passwords before an optimistic message is rendered", () => {
    const input = "IP: 192.0.2.10 用户名: root 密码: deploy-secret 帮我部署网站";
    const output = redactSensitiveTextForDisplay(input);
    expect(output).not.toContain("deploy-secret");
    expect(output).toContain("密码: [已隐藏，发送后托管]");
    expect(output).toContain("192.0.2.10");
    expect(output).toContain("root");
  });

  test("preserves opaque credential references and hides common tokens", () => {
    expect(redactSensitiveTextForDisplay("密码: echocode-credential://ssh-example"))
      .toContain("echocode-credential://ssh-example");
    expect(redactSensitiveTextForDisplay("Authorization: Bearer abcdefghijklmnop"))
      .not.toContain("abcdefghijklmnop");
    expect(redactSensitiveTextForDisplay("key sk-1234567890abcdef"))
      .not.toContain("sk-1234567890abcdef");
  });
});
