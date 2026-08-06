select cpu_id, load_factor
from sys.dm_os_schedulers
where status = 'VISIBLE ONLINE'
order by 1