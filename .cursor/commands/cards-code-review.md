# code-review-basic

Let's review recent work on this project:
- look at recent git activity
- identify any staged or uncommitted changes
- identify the cards recently worked on `cards feed --limit 10 --board engineering`
- match the pending code changes to the cards in scope

For each card in scope:

- verify the code matches the intended task and is complete and proven working (tests, direct checks)
- verify the task specified on the cards is done and working as expected, and if not, create a new card for followup work
- check that cards have adequate information and include comments or screenshots or work notes

Review the pending code in scope for general best practices:
- function/method/class naming style
- argument and parameter style, naming and types, handing of default/empty values
- data structures and schema decisions, database and API usage
- related documentation updates and following of conventions
- tests or evidence-based proof of correctness where needed
- exception and error handling and logging style
- general quality: reasonable approach, pragmatic handling of exceptions, logic implementation, maintainability, consistency, etc.

Use subagents to explore different areas or review code in depth. Run the subagents in parallel or sequentially as needed.

Collect and prioritize the results:
- Fix issues and improvements that seem clear and easy to address, defer other improvements like general refactoring or further investigation.
- Update any existing related cards that are impacted by the findings.
- discard inputs that seem picky, not reliable or not worth the effort
- create new cards for future tasks that need followups

Finally, display a brief summary of findings and changes with a short explanation for each item in plain language:
- main issues and improvements made
- topics that were updated
- new cards for future tasks as followups
- overall assessment of the codebase and project health

This command will be available in chat with /code-review-basic
