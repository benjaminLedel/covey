-- Back to the old spelling: a binary from before the rename carries the old
-- constant, and its check would reject "covey Doctor" the same way.
UPDATE agents
   SET display_name = 'Covey Doctor',
       updated_at   = now()
 WHERE slug = 'covey-doctor'
   AND display_name = 'covey Doctor';
