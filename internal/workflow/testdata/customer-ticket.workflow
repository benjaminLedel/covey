# The preview's example (#560): a ticket from assignment to the customer's test.
workflow: Customer ticket
scope: gitlab acme/timetable
owner: alex
mode: steer

roles:
  Engineering: [ada, kim, ole]
  Code-Review: [theo]
  Test: [rosa]

steps:
  t:
    type: trigger
    title: Ticket assigned
    next: b
  b:
    type: agent
    title: Build
    role: Engineering
    pick: load
    sticky: true
    max_parallel: 2
    task: "Implement {{case.title}}. Earlier defects: {{a.defects}}"
    result: [mr]
    limit: 4h
    next: r
  r:
    type: agent
    title: Review code
    role: Code-Review
    optional: true
    not_same_as: b
    task: "Review {{b.mr}}"
    result: [verdict]
    next: a
  a:
    type: agent
    title: Acceptance
    role: Test
    not_same_as: b
    sticky: true
    max_parallel: 3
    task: "Test {{b.mr}} against {{case.title}}"
    result: [result, defects]
    limit: 4h
    next: c
  c:
    type: condition
    title: Accepted?
    on: a.result
    max_rounds: 3
    next:
      accepted: s
      defects: b
      limit: h
  s:
    type: action
    title: Ticket to Alex
    as: Engineering
    system: gitlab
    do: assign
    params:
      issue_iid: "{{case.no}}"
      assignee: alex
      note: "Accepted, {{b.mr}}. Please start the customer test."
    next: w
  w:
    type: wait
    title: Customer test
    until: label:customer-ok
    max: 14d
    next:
      out: e
      timeout: n
  e:
    type: end
    title: Done
  h:
    type: approval
    title: Alex decides
    who: alex
    ask: "Three rounds without acceptance on {{case.no}}. Last defects: {{a.defects}}"
    next:
      retry: b
      self: n
  n:
    type: notify
    title: Let them know
    to: alex
    via: chat
    text: "{{case.no}} is with you now."
    next: e
