-- SQLite cannot drop columns in older versions; reset to empty array
UPDATE tasks SET attachments = '[]';
