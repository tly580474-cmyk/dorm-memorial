UPDATE notifications
SET target_type = 'message',
    target_id = substr(event_key, 9, 32)
WHERE target_type = 'conversation'
  AND event_key LIKE 'message:%'
  AND EXISTS (
      SELECT 1 FROM messages
      WHERE messages.id = substr(notifications.event_key, 9, 32)
  );
