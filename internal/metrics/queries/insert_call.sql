INSERT INTO calls (id, session_id, timestamp, provider, model,
    input_tokens, output_tokens, latency_ms, status, tool_calls)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
