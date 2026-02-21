SELECT COALESCE(SUM(input_tokens), 0),
       COALESCE(SUM(output_tokens), 0),
       COUNT(*)
FROM calls
WHERE timestamp >= ?
