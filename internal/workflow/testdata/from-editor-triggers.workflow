workflow: "Customer ticket"
owner: "admin"
mode: steer

roles:
  "Engineering": []
  "Code-Review": []
  "Test": []

steps:
  t:
    type: trigger
    title: "Ticket assigned"
    as: "Engineering"
    every: "15m"
    only_if: "gitlab:issues:assignee:builder:acme/app"
    webhook: "zammad"
    token: true
    next: b
  b:
    type: agent
    title: "Build"
    role: "Engineering"
    sticky: true
    max_parallel: 2
    task: "Implement {{case.title}}. Earlier defects: {{a.defects}}"
    result: [mr]
    limit: "4h"
    next: r
  r:
    type: agent
    title: "Review code"
    role: "Code-Review"
    not_same_as: "b"
    optional: true
    task: "Review {{b.mr}}."
    result: [verdict]
    limit: "2h"
    next: a
  a:
    type: agent
    title: "Acceptance"
    role: "Test"
    sticky: true
    max_parallel: 3
    not_same_as: "b"
    task: "Test {{b.mr}} against {{case.title}}. Report result: accepted or defects."
    result: [result, defects]
    limit: "4h"
    next: c
  c:
    type: condition
    title: "Accepted?"
    on: "a.result"
    max_rounds: 3
    next:
      "accepted": w
      "defects": b
      "limit": h
  h:
    type: approval
    title: "What next?"
    who: "admin"
    ask: "Three rounds without acceptance on {{case.no}}. Last defects: {{a.defects}}"
    next:
      "retry": b
      "stop": e
  w:
    type: wait
    title: "Customer test"
    until: "label:customer-ok"
    max: "14d"
    next:
      "out": s
      "timeout": e
  e:
    type: end
    title: "Done"
  s:
    type: action
    title: "Close"
    as: "Test"
    system: "zammad"
    do: "set_state"
    params:
      "state": "closed"
      "ticket_id": "{{case.no}}"
    next: e

layout:
  t: [40, 80]
  b: [330, 80]
  r: [620, 80]
  a: [910, 80]
  c: [1200, 80]
  h: [1490, 80]
  w: [1490, 250]
  e: [1780, 80]
  s: [1500, 400]
