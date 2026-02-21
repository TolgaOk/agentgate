SELECT DATE(timestamp) AS day,
       SUM(input_tokens),
       SUM(output_tokens),
       COUNT(*)
FROM calls
WHERE timestamp >= ?
GROUP BY day
ORDER BY day
