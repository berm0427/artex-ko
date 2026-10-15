import { activeMention, mentionSearch, mentionToken, selectedMentions } from "./chat-mentions.ts";
import assert from "node:assert/strict";
import test from "node:test";

test("mention trigger supports Korean and cursor placement without hijacking email", () => {
  assert.equal(activeMention("user@example.com", 16), null);
  assert.equal(activeMention("선택 @[취약점#1 X]", 13), null);
  assert.deepEqual(activeMention("확인@취약점 다음 문장", 6), { start: 2, end: 6, query: "취약점" });
  assert.equal(activeMention("@취약점\n다음 줄", 9), null);
});

test("categories, Korean labels, IP and keyword search", () => {
  assert.equal(mentionSearch("").categories.length, 9);
  assert.equal(mentionSearch("취").categories[0].kind, "finding");
  assert.equal(mentionSearch("취약점").kind, "finding");
  assert.equal(mentionSearch("취약점 SQL 주입").query, "SQL 주입");
  assert.equal(mentionSearch("ip 192.0.2.1").kind, "ip");
  assert.equal(mentionSearch("엔드포인트 GET /api").query, "GET /api");
  assert.equal(mentionSearch("acme.com").kind, "");
});

test("tokens roundtrip labels and removing one reference preserves its neighbors", () => {
  const first = mentionToken({ kind: "finding", id: 12, label: "제목[1]\n설명" });
  const second = mentionToken({ kind: "ip", id: 13, label: "192.0.2.1" });
  const value = `분석 ${first} 및 ${second}`;
  const selected = selectedMentions(value);
  assert.equal(selected.length, 2);
  assert.equal(selected[0].kind, "finding");
  assert.equal(selected[0].id, "12");
  assert.equal(selected[0].text, "제목（1） 설명");
  assert.equal(selected[1].kind, "ip");
  const next = value.slice(0, selected[0].start) + value.slice(selected[0].start + selected[0].token.length);
  assert.equal(selectedMentions(next)[0].token, second);
  assert.equal(selectedMentions("@[漏洞#12 기존 기록]")[0].kind, "finding");
});
