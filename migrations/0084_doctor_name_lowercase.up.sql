-- The operations engineer's fixed name follows the brand's spelling.
--
-- The name is not an organisation's choice: it belongs to the platform, so
-- that an agent allowed to read every colleague and propose changes for it is
-- recognisable as such everywhere (internal/agents, DoctorName). The API
-- enforces that — a rename to anything other than the constant is a 409.
--
-- When the constant became "covey Doctor", nothing moved the rows that were
-- already there. On an upgraded instance the doctor kept "Covey Doctor" in the
-- database, and the check then rejected the agent's OWN CURRENT NAME: every
-- client that reads the profile and writes it back unchanged ran into "the
-- operations engineer is always called covey Doctor". The name was frozen
-- against the one value it was allowed to have.
--
-- So the rows follow the constant. Only the reserved slug is touched, and only
-- where the old spelling actually stands — an organisation that never had a
-- doctor sees nothing here.
UPDATE agents
   SET display_name = 'covey Doctor',
       updated_at   = now()
 WHERE slug = 'covey-doctor'
   AND display_name = 'Covey Doctor';
