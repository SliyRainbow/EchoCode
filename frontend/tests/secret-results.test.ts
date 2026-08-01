import { describe, expect, test } from "bun:test";

import { groupSecretResults, secretResultFieldLabel, type SecretResultPart } from "../src/lib/secret-results";

describe("protected credential grouping", () => {
  test("groups an account and password from the same login into one card", () => {
    const parts: SecretResultPart[] = [
      { kind: "secret_result", secretId: "account", secretLabel: "sub2api 管理员登录账号", secretSource: "ssh://root@example.test" },
      { kind: "secret_result", secretId: "password", secretLabel: "sub2api 管理员登录密码", secretSource: "ssh://root@example.test" },
    ];
    const groups = groupSecretResults(parts);
    expect(groups).toHaveLength(1);
    expect(groups[0].title).toBe("sub2api 管理员登录凭据");
    expect(groups[0].parts).toEqual(parts);
    expect(groups[0].parts.map(secretResultFieldLabel)).toEqual(["账号", "密码"]);
  });

  test("does not merge unrelated protected fields", () => {
    const groups = groupSecretResults([
      { kind: "secret_result", secretId: "password", secretLabel: "管理员密码", secretSource: "ssh://root@example.test" },
      { kind: "secret_result", secretId: "token", secretLabel: "部署令牌", secretSource: "ssh://root@example.test" },
    ]);
    expect(groups).toHaveLength(2);
  });

  test("keeps account before password when parallel results arrive out of order", () => {
    const groups = groupSecretResults([
      { kind: "secret_result", secretId: "password", secretLabel: "sub2api 管理员登录密码", secretSource: "ssh://root@example.test" },
      { kind: "secret_result", secretId: "account", secretLabel: "sub2api 管理员登录账号", secretSource: "ssh://root@example.test" },
    ]);
    expect(groups).toHaveLength(1);
    expect(groups[0].parts.map((part) => part.secretId)).toEqual(["account", "password"]);
  });
});
